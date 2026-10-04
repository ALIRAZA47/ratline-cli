package mysql

import (
	"net"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// ShellOptions shapes one `db shell --engine mysql` session.
type ShellOptions struct {
	// Database selects the session's default schema. Optional; validated here.
	Database string
	// Execute, when set, runs this SQL and exits instead of opening a prompt. It is
	// written to the client's stdin, never passed as -e: an operator's SQL is as
	// likely as ratline's own to carry an IDENTIFIED BY, and argv is world-readable.
	Execute string
	// Term is the operator's TERM, already sanitised by the caller.
	Term string
}

// ShellCommand builds the mysql invocation for `db shell`, connected with the admin
// credentials ratline stores at paths.mysql_defaults_file.
//
// That file is already what every other MySQL operation hands the client: a 0600,
// root-owned [client] section, refused by AdminDefaultsFile if any other account can
// read it. The session reads the same file rather than a copy, so there is no second
// place the password exists, even briefly. --defaults-extra-file has to be the first
// argument or the client ignores it.
func (m *Manager) ShellCommand(opts ShellOptions) (system.Cmd, string, error) {
	if opts.Database != "" {
		if err := validate.MySQLDatabaseName(opts.Database); err != nil {
			return system.Cmd{}, "", err
		}
	}
	if m.Bins != nil && !m.Bins.Available("mysql") {
		return system.Cmd{}, "", rlerr.Preconditionf("the mysql client is not installed").
			WithHint("apt-get install mysql-client (or mariadb-client)")
	}
	path, err := m.AdminDefaultsFile()
	if err != nil {
		return system.Cmd{}, "", err
	}

	args := []string{"--defaults-extra-file=" + path}
	cmd := system.Cmd{Name: "mysql", Attach: true, Mutates: true, Label: "mysql"}
	if opts.Execute != "" {
		// With SQL on stdin the client is in batch mode, whose tab-separated output is
		// for programs. --table gives the operator the grid a prompt would have.
		// A missing final semicolon is fine: batch mode runs what is left at EOF.
		args = append(args, "--table")
		cmd.Stdin = strings.NewReader(opts.Execute + "\n")
	}
	if opts.Database != "" {
		// One element, so a name can never be read as an option of its own.
		args = append(args, "--database="+opts.Database)
	}
	cmd.Args = args

	var env []string
	if opts.Term != "" {
		env = append(env, "TERM="+opts.Term)
	}
	cmd.Env = system.MinimalEnv(env...)

	host, port := m.adminHostPort()
	return cmd, net.JoinHostPort(strings.Trim(host, `"`), strings.Trim(port, `"`)), nil
}
