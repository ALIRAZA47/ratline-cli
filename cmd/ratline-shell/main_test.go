package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper is what makes site scope mean anything, so its refusals are tested
// directly rather than only through the integration harness.

func TestParseFlags(t *testing.T) {
	ok, err := parseFlags([]string{"--site", "example.com", "--allow-shell"})
	if err != nil {
		t.Fatalf("parseFlags = %v", err)
	}
	if ok.site != "example.com" || !ok.allowShell {
		t.Errorf("parsed = %+v", ok)
	}
	// The site name comes from a forced command only root can write, but checking
	// it costs nothing and a traversal there would defeat the containment.
	for _, bad := range [][]string{
		{},
		{"--site"},
		{"--site", "../etc"},
		{"--site", "a/b"},
		{"--site", "a b"},
		{"--site", "x", "--unknown"},
	} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("parseFlags accepted %v", bad)
		}
	}
}

// Flags that would turn an allowed program into an arbitrary one.
func TestForbiddenFlag(t *testing.T) {
	cases := map[string][]string{
		"--rsh runs a command":     {"rsync", "--server", "--rsh=/bin/sh"},
		"-e is --rsh":              {"rsync", "--server", "-e", "sh"},
		"--daemon":                 {"rsync", "--server", "--daemon"},
		"--remote-option smuggles": {"rsync", "--server", "--remote-option=--rsh=sh"},
		"a remote git program":     {"git-upload-pack", "--upload-pack=/bin/sh"},
		"rsync without --server":   {"rsync", "-av", "."},
	}
	for why, argv := range cases {
		if reason := forbiddenFlag(filepath.Base(argv[0]), argv); reason == "" {
			t.Errorf("forbiddenFlag allowed %s: %v", why, argv)
		}
	}
	// The one shape a real rsync-over-SSH session uses.
	if reason := forbiddenFlag("rsync", []string{"rsync", "--server", "-vlogDtpre.iLsfxC", ".", "."}); reason != "" {
		t.Errorf("a legitimate rsync --server was refused: %s", reason)
	}
}

// The check that actually confines the session: every path argument must resolve
// inside the site directory, after symlinks.
func TestEscapingPath(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "public"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Inside is fine, including a path that does not exist yet — an upload creates
	// its target.
	for _, argv := range [][]string{
		{"rsync", "--server", "."},
		{"rsync", "--server", "public"},
		{"rsync", "--server", "public/new-file.txt"},
		{"rsync", "--server", real},
		{"rsync", "--server", "-vlogDtpre.iLsfxC", "."},
	} {
		if bad := escapingPath(real, argv); bad != "" {
			t.Errorf("%v: rejected %q, which is inside the site", argv, bad)
		}
	}

	// Outside is not.
	for _, argv := range [][]string{
		{"rsync", "--server", "/etc/passwd"},
		{"rsync", "--server", ".."},
		{"rsync", "--server", "../sibling"},
		{"rsync", "--server", "public/../../elsewhere"},
	} {
		if bad := escapingPath(real, argv); bad == "" {
			t.Errorf("%v: accepted a path outside the site", argv)
		}
	}

	// And a symlink planted inside must not be a way out — this is why the check
	// resolves rather than only cleaning.
	outside := filepath.Join(filepath.Dir(real), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if bad := escapingPath(real, []string{"rsync", "--server", "escape/file"}); bad == "" {
		t.Error("a symlink out of the site directory was followed")
	}
}

// rsync in --server mode carries filesystem paths as --flag=VALUE options. The bare-argument
// check skips anything starting with '-', so without inspecting the value a confined key
// could read or write outside the site through --copy-dest=, --temp-dir=, --log-file= and
// friends — the exact escape the confinement exists to prevent.
func TestEscapingPathChecksAttachedOptionValues(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "public"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Each of these points a path-bearing option outside the site and must be refused.
	for _, argv := range [][]string{
		{"rsync", "--server", "--copy-dest=/etc", ".", "."},
		{"rsync", "--server", "--link-dest=/home/victim/.ssh", ".", "."},
		{"rsync", "--server", "--compare-dest=../sibling", ".", "."},
		{"rsync", "--server", "--temp-dir=/tmp", ".", "."},
		{"rsync", "--server", "--partial-dir=/etc", ".", "."},
		{"rsync", "--server", "--backup-dir=/var/tmp", ".", "."},
		{"rsync", "--server", "--files-from=/etc/passwd", ".", "."},
		{"rsync", "--server", "--log-file=/home/victim/.ssh/authorized_keys", ".", "."},
	} {
		if bad := escapingPath(real, argv); bad == "" {
			t.Errorf("%v: accepted an option pointing outside the site", argv)
		}
	}

	// Confined paths and non-path values are still allowed — the check must not break a
	// legitimate transfer.
	for _, argv := range [][]string{
		{"rsync", "--server", "--partial-dir=.rsync-partial", ".", "."},
		{"rsync", "--server", "--temp-dir=public", ".", "."},
		{"rsync", "--server", "--block-size=131072", ".", "."},
		{"rsync", "--server", "--compress-level=6", ".", "."},
		{"rsync", "--server", "--out-format=%i %n%L", ".", "."},
	} {
		if bad := escapingPath(real, argv); bad != "" {
			t.Errorf("%v: rejected %q, which is inside the site or not a path", argv, bad)
		}
	}
}

func TestMatchesPreset(t *testing.T) {
	cases := []struct {
		program, preset string
		want            bool
	}{
		{"internal-sftp", "sftp-only", true},
		{"rsync", "sftp-only", false},
		{"rsync", "rsync-only", true},
		{"git-receive-pack", "git-only", true},
		{"rsync", "git-only", false},
		{"anything", "", true},
	}
	for _, tc := range cases {
		if got := matchesPreset(tc.program, tc.preset); got != tc.want {
			t.Errorf("matchesPreset(%q, %q) = %v, want %v", tc.program, tc.preset, got, tc.want)
		}
	}
}

func TestAllowedProgramsIsNarrow(t *testing.T) {
	// Anything not on the list is denied; there is no pass-through case. A shell
	// appearing here would make the whole wrapper pointless.
	for _, forbidden := range []string{"sh", "bash", "curl", "wget", "python3", "sudo", "env"} {
		if allowedPrograms[forbidden] {
			t.Errorf("%q is on the allow-list", forbidden)
		}
	}
	for _, expected := range []string{"internal-sftp", "rsync", "git-upload-pack", "git-receive-pack"} {
		if !allowedPrograms[expected] {
			t.Errorf("%q is missing from the allow-list", expected)
		}
	}
}

func TestRemoteIP(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "203.0.113.19 54321 10.0.0.1 22")
	if got := remoteIP(); got != "203.0.113.19" {
		t.Errorf("remoteIP = %q", got)
	}
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "198.51.100.4 1234 22")
	if got := remoteIP(); !strings.HasPrefix(got, "198.51.100.4") {
		t.Errorf("remoteIP = %q", got)
	}
}
