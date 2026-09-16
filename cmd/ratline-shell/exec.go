package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// exec mode is what a site's systemd unit starts.
//
// systemd reads EnvironmentFile= as PID 1, as root, before it drops to User=. That is
// how a 0600 .env can hold secrets — and it is also a root read of a path inside a
// directory the tenant owns. A tenant who replaces their .env with a symlink to another
// tenant's, then crash-loops their own service so systemd re-reads it, has root load
// somebody else's DATABASE_URL into their process environment. StandardOutput=append:
// has the same shape: PID 1 opens a path in the tenant's logs directory as root, and a
// symlink there is a root-controlled append to any file on the box.
//
// Both jobs move here, into a process that already runs as the service's own user. It
// reads the environment file and opens the log with the tenant's privileges — so the
// most a hostile .env or logs/ can do is what the tenant could have done anyway — and
// then execs the real program in place, so nothing of the wrapper remains in the data
// path.
//
//	ratline-shell exec [--env-file PATH] [--log-file PATH] -- PROGRAM [ARG...]
//
// A missing environment file is not an error, matching EnvironmentFile=-PATH. The
// file's values override the unit's Environment= lines, matching systemd's order.
type execOptions struct {
	envFile string
	logFile string
	argv    []string
}

func parseExecFlags(args []string) (execOptions, error) {
	var opts execOptions
	i := 0
	for ; i < len(args); i++ {
		switch {
		case args[i] == "--":
			i++
			opts.argv = args[i:]
			i = len(args)
		case args[i] == "--env-file":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--env-file needs a value")
			}
			i++
			opts.envFile = args[i]
		case strings.HasPrefix(args[i], "--env-file="):
			opts.envFile = strings.TrimPrefix(args[i], "--env-file=")
		case args[i] == "--log-file":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--log-file needs a value")
			}
			i++
			opts.logFile = args[i]
		case strings.HasPrefix(args[i], "--log-file="):
			opts.logFile = strings.TrimPrefix(args[i], "--log-file=")
		default:
			return opts, fmt.Errorf("unknown argument %q (the program comes after --)", args[i])
		}
	}
	if len(opts.argv) == 0 || opts.argv[0] == "" {
		return opts, fmt.Errorf("no program to run: expected -- PROGRAM [ARG...]")
	}
	for _, p := range []string{opts.envFile, opts.logFile} {
		if p != "" && !filepath.IsAbs(p) {
			return opts, fmt.Errorf("%q is not an absolute path", p)
		}
	}
	return opts, nil
}

// exitExec is the code systemd itself uses when it cannot execute a service's
// program, so a failure here reads the same way in `systemctl status`.
const exitExec = 203

func runExec(args []string) int {
	opts, err := parseExecFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell exec: %v\n", err)
		return exitBadArgs
	}

	env := os.Environ()
	if opts.envFile != "" {
		data, err := os.ReadFile(opts.envFile)
		switch {
		case err == nil:
			pairs, warnings := parseEnvFile(data)
			for _, w := range warnings {
				fmt.Fprintf(os.Stderr, "ratline-shell exec: %s: %s\n", opts.envFile, w)
			}
			env = mergeEnv(env, pairs)
		case os.IsNotExist(err):
			// EnvironmentFile=-PATH semantics: nothing to load is not a failure.
		default:
			fmt.Fprintf(os.Stderr, "ratline-shell exec: cannot read %s: %v\n", opts.envFile, err)
			return exitExec
		}
	}

	program, err := resolveProgram(opts.argv[0], lookupEnv(env, "PATH"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell exec: %v\n", err)
		return exitExec
	}

	if opts.logFile != "" {
		// Opened as this user, appended, created if missing. The unit's UMask decides
		// the mode of a new file, exactly as it did for systemd's append:.
		f, err := os.OpenFile(opts.logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ratline-shell exec: cannot open the log %s: %v\n", opts.logFile, err)
			return exitExec
		}
		// unix.Dup2 rather than syscall.Dup2: linux/arm64 has no dup2 system call,
		// only dup3, and the standard library does not paper over that — the first
		// arm64 build of this file failed to compile. x/sys provides Dup2 on every
		// platform ratline builds for, via dup3 where it has to.
		for _, fd := range []int{1, 2} {
			if err := unix.Dup2(int(f.Fd()), fd); err != nil {
				fmt.Fprintf(os.Stderr, "ratline-shell exec: redirecting output: %v\n", err)
				return exitExec
			}
		}
		f.Close()
	}

	argv := append([]string{program}, opts.argv[1:]...)
	if err := syscall.Exec(program, argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell exec: cannot run %s: %v\n", program, err)
		return exitExec
	}
	return exitOK
}

// parseEnvFile reads the KEY=VALUE format systemd's EnvironmentFile= accepts.
//
// Blank lines and lines beginning with # or ; are ignored. A value may be quoted with
// single or double quotes, in which case it runs to the matching quote and a backslash
// escapes the next character; unquoted, it runs to the end of the line with trailing
// whitespace removed and a backslash escaping the next character. A backslash at the end
// of a line continues the value on the next. A line that does not fit — no =, a name
// that is not a variable name — is reported and skipped rather than guessed at, which is
// what systemd does too.
func parseEnvFile(data []byte) (pairs [][2]string, warnings []string) {
	lines := splitContinuedLines(data)
	for n, raw := range lines {
		line := strings.TrimLeft(raw, " \t")
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validEnvName(key) {
			warnings = append(warnings, fmt.Sprintf("line %d is not KEY=VALUE and was ignored", n+1))
			continue
		}
		value, err := unquoteEnvValue(rest)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d (%s): %v; ignored", n+1, key, err))
			continue
		}
		if strings.IndexByte(value, 0) >= 0 {
			warnings = append(warnings, fmt.Sprintf("line %d (%s) contains a NUL byte; ignored", n+1, key))
			continue
		}
		pairs = append(pairs, [2]string{key, value})
	}
	return pairs, warnings
}

// splitContinuedLines joins a line ending in a backslash with the next one.
func splitContinuedLines(data []byte) []string {
	var out []string
	var cur strings.Builder
	for _, l := range bytes.Split(data, []byte("\n")) {
		s := strings.TrimRight(string(l), "\r")
		if strings.HasSuffix(s, "\\") && !strings.HasSuffix(s, "\\\\") {
			cur.WriteString(strings.TrimSuffix(s, "\\"))
			continue
		}
		cur.WriteString(s)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func unquoteEnvValue(s string) (string, error) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", nil
	}
	var b strings.Builder
	switch q := s[0]; q {
	case '"', '\'':
		i := 1
		for ; i < len(s); i++ {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				continue
			}
			if c == q {
				break
			}
			b.WriteByte(c)
		}
		if i >= len(s) {
			return "", fmt.Errorf("unterminated quote")
		}
		if tail := strings.TrimSpace(s[i+1:]); tail != "" && tail[0] != '#' && tail[0] != ';' {
			return "", fmt.Errorf("unexpected text after the closing quote")
		}
		return b.String(), nil
	default:
		s = strings.TrimRight(s, " \t")
		for i := 0; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				i++
			}
			b.WriteByte(s[i])
		}
		return b.String(), nil
	}
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// mergeEnv applies pairs over base, later values replacing earlier ones by name.
func mergeEnv(base []string, pairs [][2]string) []string {
	index := map[string]int{}
	out := make([]string, 0, len(base)+len(pairs))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if i, seen := index[k]; seen {
			out[i] = kv
			continue
		}
		index[k] = len(out)
		out = append(out, kv)
	}
	for _, p := range pairs {
		kv := p[0] + "=" + p[1]
		if i, seen := index[p[0]]; seen {
			out[i] = kv
			continue
		}
		index[p[0]] = len(out)
		out = append(out, kv)
	}
	return out
}

func lookupEnv(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

// resolveProgram finds the executable for a unit's ExecStart the way systemd would: a
// name with a slash is a path; a bare name is looked up on the PATH the service will
// run with.
func resolveProgram(name, path string) (string, error) {
	if strings.Contains(name, "/") {
		if !filepath.IsAbs(name) {
			return "", fmt.Errorf("%q is a relative path; a unit's program must be absolute", name)
		}
		if fi, err := os.Stat(name); err != nil || !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not an executable file", name)
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q was not found on PATH", name)
}
