package user

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// SudoGrant is a narrow sudo permission for a tenant.
//
// The escape hatch exists because a real deployment occasionally needs it — a
// client whose CI restarts their own service, say. It is deliberately awkward:
// config-gated, one command at a time, never a blanket grant, and validated with
// visudo before it is installed, because a malformed sudoers file locks *every*
// sudo user out of the machine.
type SudoGrant struct {
	User     string
	Commands []string
}

// sudoersFile is the drop-in for one tenant. A file per user rather than lines in
// a shared file, so removing a grant cannot damage another one.
func (m *Manager) sudoersFile(name string) string {
	// sudo ignores any file in sudoers.d whose name contains a dot or ends in ~,
	// which would make the grant silently do nothing.
	return filepath.Join("/etc/sudoers.d", "ratline-"+strings.ReplaceAll(name, ".", "-"))
}

// GrantSudo installs a narrow sudo rule for a tenant.
func (m *Manager) GrantSudo(ctx context.Context, g SudoGrant) error {
	if !m.Cfg.Users.AllowSudo {
		return rlerr.Preconditionf("sudo grants are turned off").
			WithHint("set users.allow_sudo: true in %s first, and understand that a tenant with "+
				"sudo can reach every other tenant's files", m.Cfg.SourcePath)
	}
	if _, err := m.State.GetUser(ctx, g.User); err != nil {
		return err
	}
	if len(g.Commands) == 0 {
		return rlerr.Usagef("no commands were given").
			WithHint("ratline never installs a blanket ALL grant; name the exact commands, " +
				"for example --sudo 'systemctl restart ratline-acme-example_com.service'")
	}

	// Every command must be an absolute path to a real binary. A relative name
	// would resolve through the invoking user's PATH, which is exactly the hole
	// this is meant not to open.
	rules := make([]string, 0, len(g.Commands))
	for _, raw := range g.Commands {
		parsed, err := system.ParseCommand(raw)
		if err != nil {
			return err
		}
		program := parsed.Argv[0]
		if !filepath.IsAbs(program) {
			return rlerr.Usagef("%q must be an absolute path", program).
				WithHint("a relative name resolves through the caller's PATH, which would let the " +
					"tenant choose what runs as root")
		}
		if !m.DryRun && !system.Exists(program) {
			return rlerr.Preconditionf("%s does not exist", program)
		}
		if strings.ContainsAny(raw, `\`+"\n\r\x00") {
			return rlerr.Usagef("the command contains a character sudoers cannot represent")
		}
		// ALL=(root) NOPASSWD with the full argv pinned: a bare program name would
		// let the tenant pass any arguments to it, and `systemctl` with arbitrary
		// arguments is root.
		rules = append(rules, fmt.Sprintf("%s ALL=(root) NOPASSWD: %s",
			g.User, strings.Join(parsed.Argv, " ")))
	}

	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("# A narrow sudo grant for " + g.User + ", installed by ratline.\n")
	b.WriteString("#\n")
	b.WriteString("# Every rule pins the full argument list on purpose. A grant of just the\n")
	b.WriteString("# program name would let the tenant pass any arguments to it, and most of\n")
	b.WriteString("# these programs with arbitrary arguments are equivalent to root.\n")
	b.WriteString("#\n")
	b.WriteString("# Remove with: ratline user sudo revoke " + g.User + "\n\n")
	for _, r := range rules {
		b.WriteString(r)
		b.WriteByte('\n')
	}

	path := m.sudoersFile(g.User)
	if m.DryRun {
		m.Log.Info("would install a sudo grant", "path", path, "rules", len(rules))
		return nil
	}

	// Staged to a temporary file and validated there. visudo -cf on the real path
	// would mean a malformed file had already been installed, and a broken
	// sudoers locks every sudo user out of the machine.
	tmp, err := os.CreateTemp("/etc/sudoers.d", ".ratline-sudo-check-*")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating a temporary sudoers file")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", tmpPath)
	}
	// sudo refuses a file that is not 0440, and visudo checks the mode too.
	if err := tmp.Chmod(0o440); err != nil {
		tmp.Close()
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting the mode on %s", tmpPath)
	}
	if err := tmp.Close(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "closing %s", tmpPath)
	}

	if res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "visudo", Args: []string{"-c", "-f", tmpPath}, Label: "visudo -c",
	}); err != nil {
		detail := ""
		if res != nil {
			detail = strings.TrimSpace(res.Stdout + res.Stderr)
		}
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the generated sudoers rule is not valid, so nothing was installed").
			WithField("visudo_output", detail).
			WithHint("this is a ratline bug; sudo is unaffected")
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "installing %s", path)
	}
	m.Log.Warn("installed a sudo grant", "user", g.User, "rules", len(rules), "path", path,
		"note", "a tenant with sudo can reach every other tenant's files")
	return nil
}

// RevokeSudo removes a tenant's grant.
func (m *Manager) RevokeSudo(ctx context.Context, name string) error {
	path := m.sudoersFile(name)
	if !system.Exists(path) {
		return rlerr.Preconditionf("%s has no ratline sudo grant", name)
	}
	if m.DryRun {
		m.Log.Info("would remove the sudo grant", "path", path)
		return nil
	}
	// Only a file ratline wrote: an operator's own sudoers rule is not ours.
	if err := system.RemoveManaged(path); err != nil {
		return err
	}
	// Validated afterwards too, because removing a file can only make sudoers
	// valid — but proving it costs nothing and this is the file that matters most.
	if _, err := m.Runner.Run(ctx, system.Cmd{Name: "visudo", Args: []string{"-c"}}); err != nil {
		m.Log.Error("sudoers is not valid after removing the grant; check /etc/sudoers.d by hand")
	}
	m.Log.Info("sudo grant removed", "user", name)
	return nil
}

// SudoGrants lists the tenants with a ratline-installed grant.
func (m *Manager) SudoGrants(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	entries, err := os.ReadDir("/etc/sudoers.d")
	if err != nil {
		if os.IsNotExist(err) {
			// No sudoers.d at all, so there are no grants to list.
			return out, nil
		}
		// Otherwise the answer is unknown, and an empty map would read as "nobody has
		// sudo" — the opposite of the safe reading for a privilege audit.
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "could not read /etc/sudoers.d")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ratline-") {
			continue
		}
		path := filepath.Join("/etc/sudoers.d", e.Name())
		data, err := system.ReadFileLimit(path, 1<<20)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			user, rule, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			if err := validate.Username(user); err != nil {
				continue
			}
			out[user] = append(out[user], strings.TrimSpace(rule))
		}
	}
	return out, nil
}
