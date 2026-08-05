package system

import (
	"strings"
	"testing"
)

func TestUserEnvOverridesRatherThanDuplicating(t *testing.T) {
	// A duplicate assignment is not portably resolved. os/exec happens to keep the
	// last, but a shebang interpreter or anything reading the block itself may take
	// the first — which is how `#!/usr/bin/env node` came to exit 127 with the right
	// directory sitting in a later PATH entry.
	id := &Identity{Name: "acme", Home: "/home/acme", Shell: "/bin/bash"}
	env := UserEnv(id, "PATH=/opt/node/bin:/usr/bin", "PM2_HOME=/home/acme/.pm2")

	var paths []string
	var sawPM2 bool
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			paths = append(paths, e)
		}
		if e == "PM2_HOME=/home/acme/.pm2" {
			sawPM2 = true
		}
	}
	if len(paths) != 1 {
		t.Errorf("PATH appears %d times: %v", len(paths), paths)
	}
	if len(paths) == 1 && paths[0] != "PATH=/opt/node/bin:/usr/bin" {
		t.Errorf("PATH = %q, want the override", paths[0])
	}
	if !sawPM2 {
		t.Error("a variable the base does not define should still be added")
	}
	// The rest of the base survives.
	for _, want := range []string{"HOME=/home/acme", "USER=acme", "LANG=C.UTF-8"} {
		var found bool
		for _, e := range env {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q from the environment", want)
		}
	}
}

func TestUserEnvWithNoOverridesKeepsTheDefaultPath(t *testing.T) {
	for _, e := range UserEnv(&Identity{Name: "acme", Home: "/home/acme"}) {
		if strings.HasPrefix(e, "PATH=") && e != "PATH="+DefaultPath {
			t.Errorf("PATH = %q, want the default", e)
		}
	}
}
