package sshkey

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// The managed block markers. Everything between them is regenerated from state;
// everything outside is preserved byte for byte, so a key an operator added by
// hand is never silently deleted.
const (
	BlockBegin = "# >>> ratline managed — do not edit by hand; use `ratline key add|remove`"
	BlockEnd   = "# <<< ratline managed"
)

// File is one authorized_keys file, split into the parts ratline owns and the
// parts it does not.
type File struct {
	Path      string
	Before    []string
	After     []string
	Unmanaged []UnmanagedKey
	HadBlock  bool
}

// UnmanagedKey is a key found outside the managed block. `key audit` reports
// these; nothing removes them automatically.
type UnmanagedKey struct {
	Line        string
	Fingerprint string
	Comment     string
	LineNumber  int
}

// ReadFile splits an authorized_keys file around the managed block.
func ReadFile(path string, maxBytes int) (*File, error) {
	f := &File{Path: path}
	data, err := system.ReadFileLimit(path, int64(maxBytes))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	section := 0 // 0 = before, 1 = inside, 2 = after
	lineNo := 0
	for _, line := range lines {
		lineNo++
		switch {
		case strings.HasPrefix(line, BlockBegin):
			section = 1
			f.HadBlock = true
			continue
		case strings.HasPrefix(strings.TrimSpace(line), BlockEnd):
			section = 2
			continue
		}
		switch section {
		case 0:
			f.Before = append(f.Before, line)
		case 2:
			f.After = append(f.After, line)
		}
	}
	// Trailing blank lines would accumulate on every render.
	f.Before = trimBlank(f.Before)
	f.After = trimBlank(f.After)

	for i, line := range append(append([]string{}, f.Before...), f.After...) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		u := UnmanagedKey{Line: trimmed, LineNumber: i + 1}
		if key, _, err := Parse(trimmed, Policy{MaxLineBytes: maxBytes}); err == nil {
			u.Fingerprint = key.Fingerprint
			u.Comment = key.Comment
		}
		f.Unmanaged = append(f.Unmanaged, u)
	}
	return f, nil
}

// Render produces the whole file: the operator's content, then the managed block.
func (f *File) Render(keys []*state.Key) []byte {
	var b strings.Builder
	for _, l := range f.Before {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(f.Before) > 0 {
		b.WriteByte('\n')
	}

	b.WriteString(BlockBegin)
	b.WriteByte('\n')
	for _, k := range keys {
		b.WriteString(metadataComment(k))
		b.WriteByte('\n')
		if k.Options != "" {
			b.WriteString(k.Options)
			b.WriteByte(' ')
		}
		b.WriteString(k.Algorithm)
		b.WriteByte(' ')
		b.WriteString(k.Blob)
		if k.Comment != "" {
			b.WriteByte(' ')
			b.WriteString(k.Comment)
		}
		b.WriteByte('\n')
	}
	b.WriteString(BlockEnd)
	b.WriteByte('\n')

	if len(f.After) > 0 {
		b.WriteByte('\n')
		for _, l := range f.After {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

// metadataComment records enough to trace a line in the file back to a state row
// and to a person, which is what makes the file auditable on its own.
func metadataComment(k *state.Key) string {
	parts := []string{
		"# ratline id=" + k.ID,
		`label="` + k.Label + `"`,
		"scope=" + k.Scope,
	}
	if k.Site != "" {
		parts = append(parts, "site="+k.Site)
	}
	if !k.AddedAt.IsZero() {
		parts = append(parts, "added="+k.AddedAt.UTC().Format("2006-01-02"))
	}
	if k.AddedBy != "" {
		parts = append(parts, "by="+strings.ReplaceAll(k.AddedBy, " ", "_"))
	}
	if !k.ExpiresAt.IsZero() {
		parts = append(parts, "expires="+k.ExpiresAt.UTC().Format("2006-01-02"))
	}
	return strings.Join(parts, " ")
}

// Write replaces the file atomically, preserving its mode and ownership.
//
// sshd refuses a group- or world-writable authorized_keys, so an accidental mode
// change here locks the tenant out. Preserving rather than setting means ratline
// cannot cause that by having an opinion about the mode of a file it shares with
// OpenSSH.
func Write(path string, data []byte, fallbackMode fs.FileMode, uid, gid int) error {
	if !system.Exists(path) {
		return system.WriteFileAtomic(path, data, fallbackMode, uid, gid)
	}
	return system.WriteFileAtomicPreserve(path, data, fallbackMode)
}

// CheckPermissions reports the mode problems that make sshd silently ignore a
// key file. It is a common and confusing failure, so it is checked explicitly
// rather than left to be discovered in the auth log.
func CheckPermissions(homeDir, keyFile string) []string {
	var problems []string
	check := func(path string, maxPerm fs.FileMode, what string) {
		fi, err := os.Stat(path)
		if err != nil {
			return
		}
		if fi.Mode().Perm()&^maxPerm != 0 {
			problems = append(problems, fmt.Sprintf("%s is mode %04o; sshd requires no more than %04o for %s",
				path, fi.Mode().Perm(), maxPerm, what))
		}
	}
	check(homeDir, 0o755, "a home directory")
	check(strings_Dir(keyFile), 0o700, "an .ssh directory")
	check(keyFile, 0o600, "an authorized_keys file")
	return problems
}

func strings_Dir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// RenderRevoked produces the RevokedKeys file, which sshd consults as a backstop
// even if a key somehow survives in an authorized_keys file.
func RenderRevoked(keys []*state.Key) []byte {
	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("# Keys revoked with `ratline key revoke`. sshd refuses these regardless of\n")
	b.WriteString("# what any authorized_keys file says.\n")
	for _, k := range keys {
		if k.RevokedAt.IsZero() {
			continue
		}
		b.WriteString("# " + k.Label + " revoked " + k.RevokedAt.UTC().Format("2006-01-02") + "\n")
		b.WriteString(k.Algorithm + " " + k.Blob + "\n")
	}
	return []byte(b.String())
}

// ErrNoBlock is returned when a caller expects a managed block and finds none.
var ErrNoBlock = rlerr.Preconditionf("the file has no ratline managed block")
