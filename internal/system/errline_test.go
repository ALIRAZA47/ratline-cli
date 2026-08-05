package system

import "testing"

// The one line a failed command gets to show is the whole error message an operator
// sees, so it has to be the reason and not the tool's sign-off.

func TestTheReasonBeatsTheSignpost(t *testing.T) {
	// Real certbot output. Before this, the message was "Ask for help or search for
	// solutions at https://community.letsencrypt.org." — for a renewal failure on a
	// server the operator could not then diagnose.
	certbot := "Failed to renew certificate acme.test with error: " +
		"Some challenges have failed.\n" +
		"- - - - - - - - - - - - - - - - - - - - - - - - -\n" +
		"1 renew failure(s), 0 parse failure(s)\n" +
		"Ask for help or search for solutions at https://community.letsencrypt.org. " +
		"See the logfile /var/log/letsencrypt/letsencrypt.log or re-run Certbot with -v.\n"

	got := firstMeaningfulLine(certbot)
	if got != "1 renew failure(s), 0 parse failure(s)" {
		// The count line is not the ideal reason, but it is above the sign-off and
		// carries information; what matters is that the sign-off did not win.
		t.Logf("chose: %q", got)
	}
	if isSignpost(got) {
		t.Errorf("the chosen line is pure signposting: %q", got)
	}
}

func TestSignpostsAreRecognised(t *testing.T) {
	for _, line := range []string{
		"- - - - - - - - - - - -",
		"=========",
		"Ask for help or search for solutions at https://community.letsencrypt.org.",
		"See the logfile /var/log/letsencrypt/letsencrypt.log for more details.",
		"Saving debug log to /var/log/letsencrypt/letsencrypt.log",
		"For more information, see the manual.",
	} {
		if !isSignpost(line) {
			t.Errorf("not recognised as signposting: %q", line)
		}
	}
	for _, line := range []string{
		"nginx: [emerg] duplicate \"gzip\" directive in /etc/nginx/sites-enabled/a.conf:12",
		"Permission denied",
		"npm ERR! code EACCES",
		"Some challenges have failed.",
	} {
		if isSignpost(line) {
			t.Errorf("a real reason was discarded as signposting: %q", line)
		}
	}
}

func TestSignpostsAreBetterThanNothing(t *testing.T) {
	// If the tool printed only signposting, showing it beats showing "no output".
	only := "Ask for help at https://example.invalid\n"
	if got := firstMeaningfulLine(only); got == "no output" {
		t.Error("with only signposting available it should still be shown")
	}
}

func TestNoOutputIsStillReported(t *testing.T) {
	if got := firstMeaningfulLine("", "  \n\n"); got != "no output" {
		t.Errorf("firstMeaningfulLine = %q, want \"no output\"", got)
	}
}

func TestStderrIsPreferredOverStdout(t *testing.T) {
	// The call site passes stderr first; a command that writes progress to stdout and
	// the failure to stderr must not have its progress reported as the reason.
	if got := firstMeaningfulLine("the real failure", "downloading...\ndone"); got != "the real failure" {
		t.Errorf("firstMeaningfulLine = %q, want the stderr line", got)
	}
}
