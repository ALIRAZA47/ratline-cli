package redis

import (
	"net"
	"net/url"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// ShellOptions shapes one `db shell --engine redis` session.
type ShellOptions struct {
	// Command, when set, runs this one command and exits instead of opening a prompt.
	// Each element is one argument to the command, e.g. {"GET", "shop:counter"}.
	Command []string
	// Term is the operator's TERM, already sanitised by the caller.
	Term string
}

// ShellCommand builds the redis-cli invocation for `db shell`, authenticated with the
// admin connection string at paths.redis_uri_file.
//
// Host, port and (when the URI names one) the ACL username are not secret and go in
// argv; the password goes in REDISCLI_AUTH, which redis-cli reads exactly as it would
// -a, without -a's argv exposure. A one-off command travels on stdin rather than as
// arguments: redis-cli runs commands from a non-terminal stdin, and a command such as
// ACL SETUSER … >password would otherwise put a password in the process table.
//
// There is no database argument. Redis has numbered databases, but ratline does not use
// them: a ratline "database" is a key prefix enforced by an ACL user, and this session
// is the admin, which sees every prefix. The caller refuses one rather than pretend.
func (m *Manager) ShellCommand(opts ShellOptions) (system.Cmd, string, error) {
	if m.Bins != nil && !m.Bins.Available("redis-cli") {
		return system.Cmd{}, "", rlerr.Preconditionf("redis-cli is not installed").
			WithHint("apt-get install redis-tools")
	}
	uri, err := m.AdminURI()
	if err != nil {
		return system.Cmd{}, "", err
	}
	host, port, password := hostPortPassword(uri)
	args := []string{"-h", host, "-p", port}
	if u, perr := url.Parse(uri); perr == nil {
		if u.User != nil && u.User.Username() != "" && u.User.Username() != "default" {
			args = append(args, "--user", u.User.Username())
		}
		if u.Scheme == "rediss" {
			args = append(args, "--tls")
		}
	}

	cmd := system.Cmd{Name: "redis-cli", Args: args, Attach: true, Mutates: true, Label: "redis-cli"}
	if len(opts.Command) > 0 {
		cmd.Stdin = strings.NewReader(QuoteCommand(opts.Command) + "\n")
	}
	var env []string
	if password != "" {
		env = append(env, "REDISCLI_AUTH="+password)
	}
	if opts.Term != "" {
		env = append(env, "TERM="+opts.Term)
	}
	cmd.Env = system.MinimalEnv(env...)
	return cmd, net.JoinHostPort(host, port), nil
}

// QuoteCommand renders one command as a line redis-cli reads from stdin, each argument
// double-quoted so a space, a quote or a newline inside one stays inside it. redis-cli
// splits stdin lines with the server's own sdssplitargs rules, under which a quoted
// argument understands exactly these escapes.
func QuoteCommand(argv []string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = `"` + r.Replace(a) + `"`
	}
	return strings.Join(parts, " ")
}
