package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// envFile is a site's .env, parsed and rendered in place.
//
// systemd's EnvironmentFile parser is not a shell: it takes KEY=VALUE lines,
// without expansion or command substitution. Writing anything it cannot represent
// would produce a service that starts with the wrong environment and no error, so
// values are validated rather than escaped.
type envFile struct {
	path   string
	lines  []string
	values map[string]string
	order  []string
	uid    int
	gid    int
}

func (g *Globals) openEnvFile(site *state.Site) (*envFile, error) {
	path := filepath.Join(g.Cfg.SiteDir(site.Owner, site.Domain), ".env")
	e := &envFile{path: path, values: map[string]string{}, uid: system.KeepUnchanged, gid: system.KeepUnchanged}
	if id, err := system.LookupIdentity(site.Owner); err == nil {
		e.uid, e.gid = id.UID, id.GID
	}

	data, err := system.ReadFileLimit(path, 1<<20)
	if err != nil {
		if os.IsNotExist(err) {
			return e, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			e.lines = append(e.lines, line)
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			e.lines = append(e.lines, line)
			continue
		}
		key = strings.TrimSpace(key)
		if _, seen := e.values[key]; !seen {
			e.order = append(e.order, key)
		}
		e.values[key] = value
	}
	return e, nil
}

func (e *envFile) set(key, value string) error {
	if err := validate.EnvKey(key); err != nil {
		return err
	}
	if err := validate.EnvValue(value); err != nil {
		return err
	}
	if validate.SensitiveEnvKeys[key] {
		return rlerr.Usagef("%s changes how the interpreter itself loads code, which ratline will not set", key).
			WithHint("if you genuinely need it, put it in the unit with a systemd drop-in")
	}
	if _, seen := e.values[key]; !seen {
		e.order = append(e.order, key)
	}
	e.values[key] = value
	return nil
}

func (e *envFile) unset(key string) bool {
	if _, ok := e.values[key]; !ok {
		return false
	}
	delete(e.values, key)
	kept := make([]string, 0, len(e.order))
	for _, k := range e.order {
		if k != key {
			kept = append(kept, k)
		}
	}
	e.order = kept
	return true
}

func (e *envFile) render() []byte {
	var b strings.Builder
	for _, l := range e.lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(e.lines) > 0 {
		b.WriteByte('\n')
	}
	for _, k := range e.order {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(e.values[k])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// write replaces the file at 0600, owned by the tenant.
func (e *envFile) write() error {
	return system.WriteFileAtomic(e.path, e.render(), 0o600, e.uid, e.gid)
}

func newSiteEnvCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage a site's environment variables",
		Long: "Values live in the site's .env, which is 0600 and owned by the tenant. systemd\n" +
			"reads it as root before dropping privileges, so the application receives values\n" +
			"nginx can never serve.\n\n" +
			"Values are masked in output unless --reveal, and redacted in the audit log.",
	}
	cmd.AddCommand(
		newEnvSetCommand(g),
		newEnvGetCommand(g),
		newEnvUnsetCommand(g),
		newEnvListCommand(g),
		newEnvImportCommand(g),
	)
	return cmd
}

func newEnvSetCommand(g *Globals) *cobra.Command {
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set <domain> KEY=VALUE [KEY=VALUE ...]",
		Short: "Set one or more variables and restart the service",
		Args:  cobra.MinimumNArgs(1),
		Example: "  ratline site env set api.example.com LOG_LEVEL=info\n\n" +
			"  # a secret, kept out of the process table and the shell history\n" +
			"  printf 'DATABASE_URL=%s' \"$url\" | ratline site env set api.example.com --stdin",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			assignments := args[1:]
			if fromStdin {
				b, err := io.ReadAll(io.LimitReader(g.Stdin, 1<<20))
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodeUsage, "reading from stdin")
				}
				for _, line := range strings.Split(string(b), "\n") {
					if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
						assignments = append(assignments, line)
					}
				}
			}
			if len(assignments) == 0 {
				return rlerr.Usagef("no assignments were given").
					WithHint("pass KEY=VALUE, or --stdin and pipe them in")
			}

			e, err := g.openEnvFile(site)
			if err != nil {
				return err
			}
			var names []string
			for _, a := range assignments {
				key, value, ok := strings.Cut(a, "=")
				if !ok {
					return rlerr.Usagef("%q is not a KEY=VALUE assignment", log.Redacted)
				}
				if err := e.set(key, value); err != nil {
					return err
				}
				names = append(names, key)
			}
			if g.DryRun {
				g.Log.Info("would set variables", "count", len(names), "keys", strings.Join(names, ", "))
				return nil
			}
			if err := e.write(); err != nil {
				return err
			}
			g.Log.Info("environment updated", "domain", site.Domain, "keys", strings.Join(names, ", "))

			// The application only sees the new values after a restart, so doing
			// it here avoids a confusing "I set it but nothing changed".
			restarted := false
			if site.Dynamic() && site.Enabled {
				mgr, err := g.siteManager(cmd.Context())
				if err != nil {
					return err
				}
				if _, err := mgr.Control(cmd.Context(), site.Domain, "restart"); err != nil {
					return err
				}
				restarted = true
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain, "keys": names, "restarted": restarted})
			}
			g.Printf("Set %d variable(s) on %s\n", len(names), site.Domain)
			if restarted {
				g.Printf("The service was restarted so the new values take effect.\n")
			} else if site.Dynamic() {
				g.Printf("The site is disabled; the values apply next time it starts.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "Read KEY=VALUE lines from stdin, keeping secrets out of argv")
	return Mutating(cmd)
}

func newEnvGetCommand(g *Globals) *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "get <domain> KEY",
		Short: "Show one variable, masked unless --reveal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			e, err := g.openEnvFile(site)
			if err != nil {
				return err
			}
			value, ok := e.values[args[1]]
			if !ok {
				return rlerr.Preconditionf("%s is not set for %s", args[1], site.Domain)
			}
			shown := value
			if !reveal {
				shown = log.Masked
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain, "key": args[1], "value": shown})
			}
			g.Println(shown)
			return nil
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "Print the real value")
	return cmd
}

func newEnvUnsetCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <domain> KEY",
		Short: "Remove a variable and restart the service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			e, err := g.openEnvFile(site)
			if err != nil {
				return err
			}
			if !e.unset(args[1]) {
				return rlerr.Preconditionf("%s is not set for %s", args[1], site.Domain)
			}
			if g.DryRun {
				g.Log.Info("would unset a variable", "key", args[1])
				return nil
			}
			if err := e.write(); err != nil {
				return err
			}
			if site.Dynamic() && site.Enabled {
				mgr, err := g.siteManager(cmd.Context())
				if err != nil {
					return err
				}
				if _, err := mgr.Control(cmd.Context(), site.Domain, "restart"); err != nil {
					return err
				}
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain, "key": args[1], "removed": true})
			}
			g.Printf("Removed %s from %s\n", args[1], site.Domain)
			return nil
		},
	}
	return Mutating(cmd)
}

func newEnvListCommand(g *Globals) *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "list <domain>",
		Short: "List variables, masked unless --reveal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			e, err := g.openEnvFile(site)
			if err != nil {
				return err
			}
			keys := append([]string(nil), e.order...)
			sort.Strings(keys)

			if g.JSON {
				out := map[string]string{}
				for _, k := range keys {
					if reveal {
						out[k] = e.values[k]
					} else {
						out[k] = log.Masked
					}
				}
				return g.EmitJSON(map[string]any{"domain": site.Domain, "env": out, "revealed": reveal})
			}
			if len(keys) == 0 {
				g.Printf("No variables set for %s\n", site.Domain)
				return nil
			}
			tbl := g.Table("key", "value")
			for _, k := range keys {
				value := log.Masked
				if reveal {
					value = e.values[k]
				}
				tbl.Row(k, value)
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			if !reveal {
				g.Printf("\nValues are masked. Pass --reveal to print them.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "Print the real values")
	return cmd
}

func newEnvImportCommand(g *Globals) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "import <domain> --file .env",
		Short: "Merge a .env file into a site's environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := RequireFlags(cmd, g, "file"); err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			data, err := system.ReadFileLimit(file, 1<<20)
			if err != nil {
				return err
			}
			e, err := g.openEnvFile(site)
			if err != nil {
				return err
			}
			var (
				imported []string
				skipped  []string
			)
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				// `export KEY=value` is common in hand-written .env files.
				trimmed = strings.TrimPrefix(trimmed, "export ")
				key, value, ok := strings.Cut(trimmed, "=")
				if !ok {
					skipped = append(skipped, fmt.Sprintf("line %d", i+1))
					continue
				}
				key = strings.TrimSpace(key)
				// Strip the quotes a shell would have removed, since systemd
				// would otherwise keep them as part of the value.
				value = strings.TrimSpace(value)
				if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
					value = value[1 : len(value)-1]
				}
				if err := e.set(key, value); err != nil {
					skipped = append(skipped, fmt.Sprintf("%s (%v)", key, err))
					continue
				}
				imported = append(imported, key)
			}
			if len(imported) == 0 {
				return rlerr.Usagef("no usable assignments were found in %s", file)
			}
			if g.DryRun {
				g.Log.Info("would import variables", "count", len(imported))
				return nil
			}
			if err := e.write(); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain, "imported": imported, "skipped": skipped})
			}
			g.Printf("Imported %d variable(s) into %s\n", len(imported), site.Domain)
			for _, s := range skipped {
				g.Printf("  skipped %s\n", s)
			}
			g.Printf("\nRestart to apply them:\n  ratline site restart %s\n", site.Domain)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "The .env file to import (required)")
	return Mutating(cmd)
}
