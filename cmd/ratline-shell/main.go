// Command ratline-shell is the forced command behind site-scoped SSH keys.
//
// sshd invokes it with the requested command in SSH_ORIGINAL_COMMAND. It decides
// whether that command is allowed, asserts that every path it touches resolves
// inside one site directory, and then execs the real program.
//
// What this is: a blast-radius and usability boundary. It stops a contractor's
// rsync from wandering into a sibling site, and it stops tooling mistakes from
// becoming incidents.
//
// What this is not: a kernel-enforced sandbox. The session still runs as the site
// owner's UID, so anything that already has code execution as that UID is not
// contained by this. SECURITY.md says so at length, and `ratline key test` repeats
// it for every key.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

const (
	exitOK      = 0
	exitDenied  = 1
	exitBadArgs = 2
)

// allowedPrograms is the whole surface a site-scoped key can reach. Anything not
// on this list is denied and logged; there is no pass-through case.
var allowedPrograms = map[string]bool{
	"internal-sftp":      true,
	"sftp-server":        true,
	"rsync":              true,
	"git-upload-pack":    true,
	"git-receive-pack":   true,
	"git-upload-archive": true,
	"scp":                true,
}

type options struct {
	site       string
	allowShell bool
	only       string
}

func main() {
	os.Exit(run())
}

func run() int {
	// Two jobs, one binary: the forced command for a scoped SSH key, and the process a
	// site's systemd unit starts. They share nothing but the install path, which is
	// the point — both have to be a root-owned executable that is already on every
	// server ratline manages.
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		return runExec(os.Args[2:])
	}
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell: %v\n", err)
		return exitBadArgs
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if u := os.Getenv("HOME"); u != "" {
			home = u
		} else {
			fmt.Fprintln(os.Stderr, "ratline-shell: cannot determine the home directory")
			return exitDenied
		}
	}
	siteDir := filepath.Join(home, opts.site)
	realSiteDir, err := filepath.EvalSymlinks(siteDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell: the site directory %s is not available\n", siteDir)
		return exitDenied
	}

	original := os.Getenv("SSH_ORIGINAL_COMMAND")
	auditor := openAudit()
	defer auditor.Close()
	record := func(result, detail string) {
		_ = auditor.Note("ratline-shell", map[string]string{
			"site":      opts.site,
			"result":    result,
			"requested": original,
			"remote":    remoteIP(),
			"detail":    detail,
		})
	}

	// An empty SSH_ORIGINAL_COMMAND means an interactive login attempt.
	if strings.TrimSpace(original) == "" {
		if !opts.allowShell {
			record("denied", "interactive login")
			fmt.Fprintf(os.Stderr,
				"This key is scoped to %s and permits sftp, rsync and git only.\n"+
					"There is no interactive shell. Use, for example:\n"+
					"  sftp %s@<server>\n"+
					"  rsync -az ./build/ %s@<server>:%s/public/\n"+
					"  git push %s@<server>:%s/app.git\n",
				opts.site, currentUser(), currentUser(), opts.site, currentUser(), opts.site)
			return exitDenied
		}
		record("allowed", "interactive shell")
		return execShell(realSiteDir, opts)
	}

	argv, err := system.ParseCommand(original)
	if err != nil {
		record("denied", "unparseable command")
		fmt.Fprintf(os.Stderr, "ratline-shell: %v\n", err)
		return exitDenied
	}

	program := filepath.Base(argv.Argv[0])
	// sshd requests the SFTP subsystem as the literal string "internal-sftp".
	if program == "internal-sftp" || strings.HasPrefix(original, "internal-sftp") {
		record("allowed", "sftp")
		return serveSFTP(realSiteDir)
	}
	if !allowedPrograms[program] {
		record("denied", "program "+program)
		fmt.Fprintf(os.Stderr,
			"ratline-shell: %q is not permitted by this key.\nAllowed: sftp, rsync, git-upload-pack, git-receive-pack, scp.\n",
			program)
		return exitDenied
	}
	if opts.only != "" && !matchesPreset(program, opts.only) {
		record("denied", "preset "+opts.only+" excludes "+program)
		fmt.Fprintf(os.Stderr, "ratline-shell: this key is restricted to %s only\n", opts.only)
		return exitDenied
	}

	// Reject the flags that would turn an allowed program into an arbitrary one.
	if reason := forbiddenFlag(program, argv.Argv); reason != "" {
		record("denied", reason)
		fmt.Fprintf(os.Stderr, "ratline-shell: %s\n", reason)
		return exitDenied
	}
	// Every path the command names must resolve inside the site directory, after
	// symlinks. This is the check that actually confines the session.
	if bad := escapingPath(realSiteDir, argv.Argv); bad != "" {
		record("denied", "path outside the site: "+bad)
		fmt.Fprintf(os.Stderr, "ratline-shell: %s is outside %s\n", bad, opts.site)
		return exitDenied
	}

	record("allowed", program)
	return execProgram(realSiteDir, program, argv.Argv)
}

func parseFlags(args []string) (options, error) {
	var opts options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--site":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--site needs a value")
			}
			i++
			opts.site = args[i]
		case "--allow-shell":
			opts.allowShell = true
		case "--only":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--only needs a value")
			}
			i++
			opts.only = args[i]
		default:
			return opts, fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if opts.site == "" {
		return opts, fmt.Errorf("--site is required")
	}
	// The site name comes from the forced command in authorized_keys, which only
	// root can write; checking it anyway costs nothing.
	if strings.ContainsAny(opts.site, "/\\ \t\n\x00") || strings.Contains(opts.site, "..") {
		return opts, fmt.Errorf("invalid site name %q", opts.site)
	}
	return opts, nil
}

// forbiddenFlag names the flags that would let an allowed program escape.
func forbiddenFlag(program string, argv []string) string {
	for _, a := range argv[1:] {
		switch {
		case a == "--rsh" || strings.HasPrefix(a, "--rsh="), a == "-e":
			return "--rsh is not permitted: it would run an arbitrary command"
		case a == "--daemon":
			return "--daemon is not permitted"
		case strings.HasPrefix(a, "--remote-option") || a == "-M":
			return "--remote-option is not permitted: it can smuggle further options"
		case a == "--config" && program == "rsync":
			return "--config is not permitted"
		case strings.HasPrefix(a, "--upload-pack"), strings.HasPrefix(a, "--receive-pack"),
			strings.HasPrefix(a, "--exec"):
			return "specifying a remote program is not permitted"
		}
	}
	if program == "rsync" {
		// The only rsync invocation an SSH session ever makes is --server.
		hasServer := false
		for _, a := range argv[1:] {
			if a == "--server" {
				hasServer = true
				break
			}
		}
		if !hasServer {
			return "only 'rsync --server' invocations are permitted over SSH"
		}
	}
	return ""
}

// escapingPath returns the first argument that names a path outside the site.
func escapingPath(root string, argv []string) string {
	for _, a := range argv[1:] {
		if a == "" {
			continue
		}
		// An option that carries its value as --flag=VALUE hides a path from the bare-arg
		// check below, which skips anything starting with '-'. rsync in --server mode passes
		// filesystem paths exactly this way — --copy-dest=, --link-dest=, --compare-dest=,
		// --temp-dir=, --partial-dir=, --backup-dir=, --files-from=, --log-file= — so a
		// confined key could otherwise read or write outside the site through one of them.
		// Check the value; a non-path value (a number, a format string, a relative name)
		// resolves harmlessly inside the site and is not flagged. The space-separated form
		// (--flag /path) is already caught, because /path is a bare argument.
		if strings.HasPrefix(a, "-") {
			if i := strings.IndexByte(a, '='); i >= 0 && pathEscapes(root, a[i+1:]) {
				return a
			}
			continue
		}
		// rsync's protocol sends "." for the transfer root, which means the cwd.
		if a == "." || a == "./" {
			continue
		}
		if pathEscapes(root, a) {
			return a
		}
	}
	return ""
}

// pathEscapes reports whether candidate, resolved against the site root and following
// symlinks in its existing prefix, lands outside it. An empty value is not a path.
func pathEscapes(root, candidate string) bool {
	if candidate == "" {
		return false
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, err := resolveDeepest(candidate)
	if err != nil {
		return true
	}
	return resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator))
}

// resolveDeepest resolves the longest existing prefix of a path, so an upload to
// a file that does not exist yet is still checked against its real parent.
func resolveDeepest(p string) (string, error) {
	cur := filepath.Clean(p)
	rest := ""
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(p), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func matchesPreset(program, preset string) bool {
	switch preset {
	case "sftp-only":
		return program == "internal-sftp" || program == "sftp-server"
	case "rsync-only":
		return program == "rsync"
	case "git-only":
		return strings.HasPrefix(program, "git-")
	default:
		return true
	}
}

func execProgram(siteDir, program string, argv []string) int {
	// The program comes from a fixed list of system directories, never from PATH. The
	// environment reaches here from sshd and is not something a scoped key should be
	// able to influence, but "should not" is not a reason to consult it: the wrapper
	// exists to run exactly the system's rsync, and a PATH entry that shadowed it would
	// be a second program with the same name.
	path := ""
	for _, dir := range []string{"/usr/bin", "/bin", "/usr/local/bin"} {
		candidate := filepath.Join(dir, program)
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() {
			path = candidate
			break
		}
	}
	if path == "" {
		fmt.Fprintf(os.Stderr, "ratline-shell: %s is not installed\n", program)
		return exitDenied
	}
	full := append([]string{path}, argv[1:]...)
	return execAt(siteDir, path, full)
}

func execShell(siteDir string, opts options) int {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	fmt.Fprintf(os.Stderr,
		"\nratline: this session is scoped to %s.\n"+
			"You have a shell because the key was added with --allow-shell, which means the\n"+
			"confinement is advisory: it runs as your own UID and can reach anything that UID\n"+
			"can. Working directory: %s\n\n", opts.site, siteDir)
	return execAt(siteDir, shell, []string{shell, "-l"})
}

// execAt replaces this process, so there is no wrapper left in the middle of the
// data path to buffer or mangle an rsync or git stream.
func execAt(dir, path string, argv []string) int {
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell: cannot enter %s\n", dir)
		return exitDenied
	}
	env := append(os.Environ(), "RATLINE_SITE_DIR="+dir)
	if err := syscall.Exec(path, argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "ratline-shell: cannot run %s: %v\n", path, err)
		return exitDenied
	}
	return exitOK
}

func openAudit() log.Auditor {
	if a, err := log.OpenAudit("/var/log/ratline/audit.log"); err == nil {
		return a
	}
	// This process runs as the tenant, and the audit file is root's, so the open above
	// fails on every scoped-key session. The journal accepts a line from any uid and
	// records which uid sent it, which is a trail the tenant can add to but not forge.
	// The session must not fail because neither is writable; the tenant cannot fix it.
	if a, err := log.SyslogAudit("ratline-shell"); err == nil {
		return a
	}
	return log.NopAudit()
}

func remoteIP() string {
	// SSH_CONNECTION is "<client ip> <client port> <server ip> <server port>".
	if c := os.Getenv("SSH_CONNECTION"); c != "" {
		if i := strings.IndexByte(c, ' '); i > 0 {
			return c[:i]
		}
	}
	return os.Getenv("SSH_CLIENT")
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return "user"
}

var _ = time.Now
