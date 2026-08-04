package sshkey

import "testing"

func TestForceCommandNoneIsNotAForcedCommand(t *testing.T) {
	// `sshd -T` prints "forcecommand none" when there is none. Reading that as a real
	// forced command made VerifyLogin refuse on any ordinary server, reverting every
	// `key add`, `key sync` and `reconcile --fix` with `a ForceCommand of "none"
	// applies` — the opposite of what the verification is for.
	for _, tc := range []struct {
		value   string
		foreign bool
	}{
		{"none", false},
		{"  none  ", false},
		{"", false},
		{"internal-sftp", false},
		{"internal-sftp -d /site", false},
		{"/usr/local/lib/ratline/ratline-shell", false},
		{"/bin/bash", true},
		{"/usr/bin/tmux attach", true},
		{"/opt/monitoring/wrapper.sh", true},
	} {
		if got := foreignForceCommand(tc.value); got != tc.foreign {
			t.Errorf("foreignForceCommand(%q) = %v, want %v", tc.value, got, tc.foreign)
		}
	}
}
