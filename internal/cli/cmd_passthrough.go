package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline nginx`, `ratline systemctl` and `ratline journalctl` — the system tools an
// operator reaches for on a ratline server, run through ratline.
//
// The point is not to wrap them for the sake of it. Each has one way of being used that
// fights ratline, and one piece of knowledge an operator has to carry in their head:
//
//   - `nginx -s reload` signals the master behind systemd's back, and `nginx -s stop`
//     leaves every site down with nothing in state saying so.
//   - `systemctl restart` wants a unit name derived from a slug nobody remembers, and
//     `systemctl enable`/`mask`/`edit` on a managed unit is drift reconcile will undo.
//   - `journalctl -u ratline-…` comes up empty for a site, because a site's units log into
//     their own namespace, and the flag that fixes it is the one nobody types.
//
// So each command supplies the binary (from the Runner's registry, never PATH), the unit
// name and the namespace, passes the rest through as an argv, and refuses the operations
// that would fight ratline with a hint naming the ratline command that does it properly.
//
// The locking is decided at run time rather than by annotation. `systemctl status` and
// `systemctl restart` are one command, and a status that waited behind a twenty-minute
// deploy for the lock — or, worse, failed with exit 5 because one was running — would be
// a read made unusable by a mutation it has nothing to do with. Both nginx and systemctl
// are therefore declared Mutating (so the schema, the panel's default policy and the
// config warning all treat them as what they can be) with SkipLock, and take the lock
// themselves, through lockNow, only on the path that changes something.

// noTimeout bounds a command that is meant to run until somebody stops it — journalctl
// --follow. The Runner treats zero as "the default two minutes", so "never" has to be a
// real duration; a century is one, and it is far enough from overflowing a time.Time.
const noTimeout = 100 * 365 * 24 * time.Hour

// passthroughReadTimeout bounds a read that is not following: `nginx -T` on a server with
// a few hundred vhosts, or a journal printed from the beginning, is slow but finite.
const passthroughReadTimeout = 10 * time.Minute

// passthroughControlTimeout bounds a systemctl control verb. A restart that has not
// returned in this long is a unit stuck in its stop timeout, and holding the server lock
// for longer would stop every renewal on the box.
const passthroughControlTimeout = 5 * time.Minute

// lockNow takes the server lock for a command whose need for it is only known once its
// arguments have been read. Released by teardown, exactly as the lock setup takes is.
//
// Never under --dry-run, for the same reason setup does not take it then: a rehearsal
// changes nothing, so it has nothing to serialise against.
func (g *Globals) lockNow() error {
	if g.DryRun || g.lock != nil || g.completionMode {
		return nil
	}
	if g.Cfg == nil {
		return rlerr.Genericf("internal error: the configuration was not loaded")
	}
	l, err := system.AcquireLock(g.Cfg.Paths.Lock, g.Cfg.Defaults.LockTimeout.D(), g.CmdPath)
	if err != nil {
		return err
	}
	g.lock = l
	return nil
}

// passthroughArgs turns the positional arguments after -- into an argv.
//
// One argument holding a whole command line is what the interactive menu and the panel
// collect — a single field — so it is split by the parser site exec uses, which never
// involves a shell and refuses a pipe. Already-split arguments are left exactly as they
// are, so `--grep='a b'` typed at a shell keeps its space.
func passthroughArgs(args []string) ([]string, error) {
	if len(args) == 1 && strings.ContainsAny(args[0], " \t") {
		return execArgv(args)
	}
	return args, nil
}

// passthroughRun runs one invocation of a system tool and reports it the way site exec
// reports a program: its output raw on the terminal, or captured into the one --json
// envelope, and a non-zero exit reported as external (exit 4) naming the code rather than
// adopted — ratline's exit codes are a contract, and systemctl exiting 3 for a stopped
// unit does not mean ratline was called wrongly.
//
// follow marks a command that runs until it is interrupted: no timeout, and Ctrl-C is how
// it ends rather than a failure.
func (g *Globals) passthroughRun(ctx context.Context, c system.Cmd, follow bool, extra map[string]any, hint func(code int) string) error {
	if follow {
		c.Timeout = noTimeout
	} else if c.Timeout <= 0 {
		c.Timeout = passthroughReadTimeout
	}
	if !g.JSON {
		c.Stdout, c.Stderr = g.Stdout, g.Stderr
	}
	res, err := g.Runner.Run(ctx, c)
	if follow && ctx.Err() != nil {
		//nolint:nilerr // Ctrl+C is how an operator ends a --follow; the cancellation is the intent, not a failure
		return nil
	}

	name := c.Name
	if name == "" {
		name = c.Path
	}
	if res != nil && err != nil && res.ExitCode != 0 {
		e := rlerr.Externalf("%s exited %d", name, res.ExitCode).
			WithField("exit_code", fmt.Sprint(res.ExitCode)).
			WithField("command", strings.Join(append([]string{name}, c.Args...), " "))
		if hint != nil {
			if h := hint(res.ExitCode); h != "" {
				e = e.WithHint("%s", h)
			}
		}
		if e.Hint == "" && g.JSON {
			e = e.WithHint("its output is in the envelope's stdout and stderr")
		}
		err = e
	}

	if g.JSON && res != nil {
		data := map[string]any{
			"program": res.Path, "args": res.Args,
			"exit_code": res.ExitCode, "duration_ms": res.Duration.Milliseconds(),
			"stdout": res.Stdout, "stderr": res.Stderr,
		}
		for k, v := range extra {
			data[k] = v
		}
		if jerr := g.EmitJSON(data); jerr != nil {
			return jerr
		}
	}
	return err
}

// passthroughUsage is the Args check every one of these commands shares: something has
// to come after --, and the commonest way of getting nothing there is typing the tool's
// own flags before it, where -v and -q are ratline's --verbose and --quiet.
func passthroughUsage(example string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return nil
		}
		return rlerr.Usagef("nothing to pass to %s", cmd.Name()).
			WithHint("its arguments go after --, as in '%s' (before the --, -v and -q are ratline's own --verbose and --quiet)", example)
	}
}

// hasGlob reports whether a name is a pattern rather than a name.
func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }
