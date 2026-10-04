package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// runAttached runs a command that owns the terminal until it exits — an interactive
// database shell — and differs from the ordinary path in four deliberate ways:
//
//   - Its stdio is passed through, not captured. A REPL needs a real terminal on its
//     descriptors to edit lines and to decide that it is interactive at all; a pipe into
//     a capture buffer turns mongosh, mysql and redis-cli into batch tools.
//   - It is not put in a process group of its own. The ordinary path does that so a
//     timeout can kill a build and its children together, but a background process
//     group that reads the terminal is stopped with SIGTTIN, so the shell would hang at
//     its first prompt. Left in the foreground group, Ctrl-C reaches the shell, which
//     uses it to abandon the current line or query.
//   - No timeout, and the context does not end it. ratline traps SIGINT to cancel its
//     context, so a context-bound child would be killed by the very Ctrl-C the person
//     meant for the shell's prompt.
//   - ratline itself ignores SIGINT and SIGQUIT for the duration (by catching them, which
//     the child does not inherit), so the parent cannot die underneath a session that
//     still holds the terminal.
//
// Everything else is the same: the binary comes from the registry, argv is validated,
// the environment is whatever the caller built (MinimalEnv when unset), and --dry-run
// has already returned before this is reached for a mutating command.
func (r *execRunner) runAttached(ctx context.Context, path string, c Cmd, label string) (*Result, error) {
	// WithoutCancel: the context still carries its values, but neither its deadline nor
	// the SIGINT that cancels it can end the session.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if cmd.Env == nil {
		if c.As != nil {
			cmd.Env = UserEnv(c.As)
		} else {
			cmd.Env = MinimalEnv()
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if c.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if c.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if c.As != nil {
		cred, err := credentialFor(c.As)
		if err != nil {
			return nil, err
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGQUIT)
	defer signal.Stop(sigs)

	r.log.Debug("run attached", "cmd", log.ArgvString(append([]string{path}, c.Args...)), "dir", c.Dir)
	start := time.Now()
	runErr := cmd.Run()
	res := &Result{Path: path, Args: c.Args, Duration: time.Since(start)}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return res, nil
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, rlerr.Wrap(runErr, rlerr.CodeExternal, "could not run %s", path).
			WithHint("check that %s exists and is executable", path)
	}
	if containsInt(c.OKExit, res.ExitCode) {
		return res, nil
	}
	// The child wrote its own reason to the terminal already; repeating a captured
	// line is not possible and not needed. The status is what is left to say.
	return res, rlerr.Externalf("%s exited with status %d", label, res.ExitCode).
		WithField("exit_code", fmt.Sprint(res.ExitCode))
}
