package mongod

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func host(id, codename string) system.OSInfo {
	return system.OSInfo{ID: id, Codename: codename, Arch: "amd64", PrettyName: id + " " + codename}
}

func TestResolveVersionPicksWhatTheRepositoryPublishes(t *testing.T) {
	cases := []struct {
		os        system.OSInfo
		requested string
		want      string
		wantErr   string
	}{
		{os: host("ubuntu", "noble"), want: "8.0"},
		{os: host("ubuntu", "jammy"), want: "8.0"},
		{os: host("debian", "bookworm"), want: "8.0"},
		// bullseye only ever got 7.0; defaulting to 8.0 there would point apt at a
		// URL that does not exist.
		{os: host("debian", "bullseye"), want: "7.0"},
		{os: host("ubuntu", "jammy"), requested: "7.0", want: "7.0"},
		{os: host("ubuntu", "noble"), requested: "7.0", wantErr: "not published"},
		{os: host("ubuntu", "warty"), wantErr: "does not publish"},
		{os: host("linuxmint", "wilma"), wantErr: "Ubuntu and Debian"},
	}
	for _, c := range cases {
		got, err := ResolveVersion(c.os, c.requested)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("ResolveVersion(%s %s, %q) error = %v, want mention of %q",
					c.os.ID, c.os.Codename, c.requested, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveVersion(%s %s, %q): %v", c.os.ID, c.os.Codename, c.requested, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveVersion(%s %s, %q) = %s, want %s", c.os.ID, c.os.Codename, c.requested, got, c.want)
		}
	}
}

func TestResolveVersionRefusesUnpublishedArchitectures(t *testing.T) {
	o := host("ubuntu", "jammy")
	o.Arch = "386"
	if _, err := ResolveVersion(o, ""); err == nil {
		t.Error("a 386 host was allowed to point apt at a repository with no 386 packages")
	}
}

func TestSourceLine(t *testing.T) {
	line, err := SourceLine(host("ubuntu", "jammy"), "8.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "deb [ arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg ] " +
		"https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/8.0 multiverse"
	if line != want {
		t.Errorf("ubuntu line = %q, want %q", line, want)
	}

	line, err = SourceLine(host("debian", "bookworm"), "7.0")
	if err != nil {
		t.Fatal(err)
	}
	// Debian files MongoDB under main, not multiverse; getting the component wrong
	// makes apt-get update fail with a message about a missing Packages file.
	if !strings.HasSuffix(line, "bookworm/mongodb-org/7.0 main") {
		t.Errorf("debian line = %q, want the main component", line)
	}
}

func TestSigningKeyExistsForEverySupportedVersion(t *testing.T) {
	// Every version the matrix can resolve must have a key in the binary — a version
	// added to the matrix without its key would fail at install time on a server,
	// which is the expensive place to find out.
	seen := map[string]bool{}
	for _, versions := range versionsByCodename {
		for _, v := range versions {
			seen[v] = true
		}
	}
	for v := range seen {
		if _, err := SigningKey(v); err != nil {
			t.Errorf("no usable embedded signing key for %s: %v", v, err)
		}
	}
	if _, err := SigningKey("1.0"); err == nil {
		t.Error("a version with no embedded key returned one anyway")
	}
}
