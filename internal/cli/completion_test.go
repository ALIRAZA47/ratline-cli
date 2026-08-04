package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCompletionNeedsNoRootAndPrintsNoDiagnostics(t *testing.T) {
	// Completion runs as whoever pressed Tab, which is usually not root, and cobra
	// reserves stdout for the candidate list. A privilege refusal here would both
	// break completion for every non-root operator and be offered to them as a
	// candidate.
	for _, args := range [][]string{
		{"__complete", "site", "show", ""},
		{"__complete", "user", "delete", ""},
		{"__complete", "cert", "renew", ""},
		{"__complete", "key", "remove", ""},
		{"__complete", "site", "add", "--runtime", ""},
	} {
		code, out, _ := harness(t, args...)
		if code != 0 {
			t.Errorf("%v exited %d, want 0", args, code)
		}
		if strings.Contains(out.String(), "must run as root") {
			t.Errorf("%v offered a privilege error as a completion:\n%s", args, out.String())
		}
		if strings.Contains(out.String(), "error:") {
			t.Errorf("%v printed an error onto the candidate list:\n%s", args, out.String())
		}
	}
}

func TestFixedFlagCompletionsMatchWhatTheFlagsAccept(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"__complete", "site", "add", "--runtime", ""}, []string{"static", "node", "python"}},
		{[]string{"__complete", "site", "add", "--daemon", ""}, []string{"pm2", "direct"}},
		{[]string{"__complete", "site", "add", "--listen", ""}, []string{"socket", "port"}},
		{[]string{"__complete", "site", "runtime", "--daemon", ""}, []string{"pm2", "direct"}},
	} {
		_, out, _ := harness(t, tc.args...)
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v did not offer %q:\n%s", tc.args, want, out.String())
			}
		}
	}
}

func TestCompletionPrefixFiltersCandidates(t *testing.T) {
	_, out, _ := harness(t, "__complete", "site", "add", "--runtime", "n")
	s := out.String()
	if !strings.Contains(s, "node") {
		t.Errorf("a prefix of 'n' should still offer node:\n%s", s)
	}
	if strings.Contains(s, "python") {
		t.Errorf("a prefix of 'n' should not offer python:\n%s", s)
	}
}

func TestEveryDomainAndUserArgumentHasCompletion(t *testing.T) {
	// The registration is a traversal over a name list, which is exactly the kind
	// of thing that goes stale when a command is renamed. This asserts that no
	// command taking a <domain> or <user> argument was missed.
	g := NewGlobals()
	root := NewRootCommand(g)

	var missing []string
	walk(root, func(cmd *cobra.Command) {
		if cmd.ValidArgsFunction != nil || !cmd.Runnable() {
			return
		}
		use := cmd.Use
		if strings.Contains(use, "<domain>") || strings.Contains(use, "<user>") {
			missing = append(missing, cmd.CommandPath())
		}
	})
	if len(missing) > 0 {
		t.Errorf("these commands take a name argument but offer no completion:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestIsCompletionRequestRecognisesTheHiddenCommands(t *testing.T) {
	g := NewGlobals()
	root := NewRootCommand(g)
	// __complete is added by cobra during Execute rather than at construction, so
	// this is the case the name check exists for.
	fake := &cobra.Command{Use: "__completeNoDesc"}
	root.AddCommand(fake)
	if !isCompletionRequest(fake) {
		t.Error("__completeNoDesc should be recognised as a completion request")
	}
	if isCompletionRequest(root) {
		t.Error("the root command is not a completion request")
	}
	sub := &cobra.Command{Use: "child"}
	fake.AddCommand(sub)
	// A descendant of the completion helper counts too, since the privilege check
	// receives the command actually being run.
	if !isCompletionRequest(sub) {
		t.Error("a child of the completion helper should be recognised")
	}
}

func TestCreationCommandsOfferNothingRatherThanExistingNames(t *testing.T) {
	// `site add <domain>` takes a name that by definition does not exist yet, so
	// offering the sites that do exist would suggest values that are all refused.
	for _, args := range [][]string{
		{"__complete", "site", "add", ""},
		{"__complete", "user", "add", ""},
	} {
		_, out, _ := harness(t, args...)
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		for _, line := range lines {
			// Only cobra's directive line is expected.
			if line != "" && !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "Completion ended") {
				t.Errorf("%v offered %q, but the name does not exist yet", args, line)
			}
		}
	}
}

func TestFilenamesAreNeverOfferedForANameArgument(t *testing.T) {
	// A domain is not a path. Falling back to the working directory's contents is
	// the cobra default and is pure noise here.
	g := NewGlobals()
	root := NewRootCommand(g)
	var offenders []string
	walk(root, func(cmd *cobra.Command) {
		if !cmd.Runnable() {
			return
		}
		for _, placeholder := range []string{"<domain>", "<username>", "<fingerprint"} {
			if strings.Contains(cmd.Use, placeholder) && cmd.ValidArgsFunction == nil {
				offenders = append(offenders, cmd.CommandPath())
			}
		}
	})
	if len(offenders) > 0 {
		t.Errorf("these would fall back to filename completion:\n  %s", strings.Join(offenders, "\n  "))
	}
}
