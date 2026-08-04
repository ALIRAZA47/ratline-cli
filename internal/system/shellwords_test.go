package system

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestParseCommandValid(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"npm run start", []string{"npm", "run", "start"}},
		{"npm ci --omit=dev", []string{"npm", "ci", "--omit=dev"}},
		{"  node   server.js  ", []string{"node", "server.js"}},
		{`node --title="my app" server.js`, []string{"node", "--title=my app", "server.js"}},
		{`node --title='my app' server.js`, []string{"node", "--title=my app", "server.js"}},
		{`node my\ file.js`, []string{"node", "my file.js"}},
		{"./bin/start", []string{"./bin/start"}},
		{"/usr/local/bin/app --port 3000", []string{"/usr/local/bin/app", "--port", "3000"}},
		{`pnpm run build -- --mode "production"`, []string{"pnpm", "run", "build", "--", "--mode", "production"}},
	}
	for _, tc := range cases {
		got, err := ParseCommand(tc.in)
		if err != nil {
			t.Errorf("ParseCommand(%q) = %v", tc.in, err)
			continue
		}
		if len(got.Argv) != len(tc.want) {
			t.Errorf("ParseCommand(%q) = %q, want %q", tc.in, got.Argv, tc.want)
			continue
		}
		for i := range tc.want {
			if got.Argv[i] != tc.want[i] {
				t.Errorf("ParseCommand(%q) = %q, want %q", tc.in, got.Argv, tc.want)
				break
			}
		}
	}
}

func TestParseCommandRejectsShellSyntax(t *testing.T) {
	// Every one of these would either need a shell or silently misbehave.
	invalid := map[string]string{
		"":                         "empty",
		"   ":                      "whitespace only",
		"npm start; reboot":        "command separator",
		"npm start && rm -rf /":    "command chaining",
		"npm start || true":        "command chaining",
		"npm start | tee log":      "pipe",
		"npm start &":              "backgrounding",
		"npm start > out.log":      "redirection",
		"npm start >> out.log":     "append redirection",
		"npm start < in.txt":       "input redirection",
		"cat <<EOF":                "here-document",
		"echo $(id)":               "command substitution",
		"echo `id`":                "backtick substitution",
		"echo ${HOME}":             "variable expansion",
		"echo $HOME":               "variable expansion",
		"npm start\nreboot":        "newline",
		"npm start\rreboot":        "carriage return",
		"npm\x00start":             "NUL byte",
		"sh -c 'npm start'":        "a shell as the program",
		"bash script.sh":           "a shell as the program",
		"env FOO=1 npm start":      "env rewrites the environment",
		"sudo npm start":           "privilege escalation",
		"xargs npm start":          "xargs reinterprets its input",
		"--port 3000":              "starts with a flag",
		`node "unterminated`:       "unterminated quote",
		`node trailing\`:           "trailing backslash",
		strings.Repeat("a ", 3000): "too long",
	}
	for in, why := range invalid {
		got, err := ParseCommand(in)
		if err == nil {
			t.Errorf("ParseCommand(%q) = %q, want an error (%s)", in, got.Argv, why)
			continue
		}
		if !rlerr.Is(err, rlerr.CodeUsage) {
			t.Errorf("ParseCommand(%q) returned code %v, want usage", in, rlerr.CodeOf(err))
		}
	}
}

func TestParseCommandTooManyWords(t *testing.T) {
	if _, err := ParseCommand("node " + strings.Repeat("a ", maxCommandWords+5)); err == nil {
		t.Error("ParseCommand accepted more words than the limit")
	}
}

func TestParseCommandWarnsAboutGlobs(t *testing.T) {
	// A glob is not dangerous without a shell, but it will not expand either,
	// which is almost certainly not what the operator meant.
	got, err := ParseCommand("node dist/*.js")
	if err != nil {
		t.Fatalf("ParseCommand = %v", err)
	}
	if len(got.Warnings) == 0 {
		t.Error("expected a warning about the glob being passed through literally")
	}
}

func TestValidateArgv(t *testing.T) {
	if err := ValidateArgv([]string{"-t", "--", "ok"}); err != nil {
		t.Errorf("ValidateArgv on normal arguments = %v", err)
	}
	for _, bad := range [][]string{
		{"a\x00b"},
		{"a\nb"},
		{"a\rb"},
		{strings.Repeat("x", 5000)},
	} {
		if err := ValidateArgv(bad); err == nil {
			t.Errorf("ValidateArgv(%q…) = nil, want an error", bad[0][:min(len(bad[0]), 8)])
		}
	}
}

func FuzzParseCommand(f *testing.F) {
	for _, seed := range []string{"npm run start", "", "sh -c x", "a $(b)", `a "b`, "a\nb", "node x.js"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, err := ParseCommand(in)
		if err != nil {
			return
		}
		if len(got.Argv) == 0 {
			t.Fatalf("ParseCommand(%q) succeeded with an empty argv", in)
		}
		// Nothing accepted may need a shell, and nothing may carry a byte that
		// execve or a unit file would reject.
		for _, op := range shellOperators {
			if strings.Contains(in, op.token) {
				t.Fatalf("accepted %q, which contains %q", in, op.token)
			}
		}
		if err := ValidateArgv(got.Argv); err != nil {
			t.Fatalf("accepted %q, producing an argv that fails validation: %v", in, err)
		}
		base := got.Program()
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if shellFirstWords[base] {
			t.Fatalf("accepted %q, whose program is %q", in, base)
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
