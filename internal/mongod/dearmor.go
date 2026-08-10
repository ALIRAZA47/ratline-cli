package mongod

import (
	"encoding/base64"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Dearmor converts an ASCII-armored OpenPGP key into the binary keyring apt reads.
//
// Done here rather than by shelling out to `gpg --dearmor` for two reasons. gpg is not
// in the binaries registry and adding it means trusting one more root-run executable
// for what is, underneath, base64. And older apt (focal's 2.0, bullseye's 2.2) does not
// accept armored keys in signed-by, so shipping the .asc unconverted would work on some
// supported hosts and break on others — the kind of difference that surfaces months
// later on the one server that is different.
//
// The CRC24 checksum is verified when present. This key ends up as a root of trust for
// packages installed as root; a corrupted decode must be an error, never a keyring.
func Dearmor(asc []byte) ([]byte, error) {
	const (
		begin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
		end   = "-----END PGP PUBLIC KEY BLOCK-----"
	)
	lines := strings.Split(string(asc), "\n")

	// Find the block, then skip its armor headers ("Version: ..."), which end at the
	// first blank line.
	i := 0
	for ; i < len(lines) && strings.TrimSpace(lines[i]) != begin; i++ {
	}
	if i == len(lines) {
		return nil, rlerr.Genericf("the embedded key is not an armored PGP public key block")
	}
	i++
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			break
		}
		// A body line never contains ':'; a header always does. Armor without any
		// headers goes straight into the body after BEGIN.
		if !strings.Contains(lines[i], ":") {
			break
		}
	}

	var body, checksum strings.Builder
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case line == end:
			i = len(lines)
		case strings.HasPrefix(line, "="):
			checksum.WriteString(line[1:])
		default:
			body.WriteString(line)
		}
	}

	raw, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "decoding the embedded key")
	}
	if len(raw) == 0 {
		return nil, rlerr.Genericf("the embedded key decoded to nothing")
	}
	// The checksum line is optional in current OpenPGP, so its absence is fine; a
	// present one that does not match is not.
	if checksum.Len() > 0 {
		want, err := base64.StdEncoding.DecodeString(checksum.String())
		if err != nil || len(want) != 3 {
			return nil, rlerr.Genericf("the embedded key's checksum line is malformed")
		}
		got := crc24(raw)
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			return nil, rlerr.Genericf("the embedded key failed its checksum")
		}
	}
	return raw, nil
}

// crc24 is the OpenPGP armor checksum (RFC 4880 section 6.1).
func crc24(data []byte) [3]byte {
	crc := uint32(0xB704CE)
	for _, b := range data {
		crc ^= uint32(b) << 16
		for i := 0; i < 8; i++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864CFB
			}
		}
	}
	return [3]byte{byte(crc >> 16), byte(crc >> 8), byte(crc)}
}
