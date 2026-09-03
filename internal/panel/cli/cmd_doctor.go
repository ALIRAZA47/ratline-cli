package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/install"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/web"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// check is one thing that can be wrong, and what to do about it.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	// Fatal separates "this panel cannot work" from "this panel is working and
	// somebody should look at this".
	Fatal bool `json:"fatal"`
}

func newDoctorCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the panel's installation and report what is wrong",
		Args:  cobra.NoArgs,
		Long: "Every check runs and every problem is reported together, rather than\n" +
			"stopping at the first: fixing one per run is a poor way to spend an\n" +
			"afternoon on a server you are already unhappy with.\n\n" +
			"Exit 0 when everything is fine, 3 when something is wrong — so a monitoring\n" +
			"system can branch on it.",
		RunE: func(cmd *cobra.Command, _ []string) error { return app.doctor(cmd.Context()) },
	}
}

func (app *App) doctor(ctx context.Context) error {
	checks := app.runChecks(ctx)

	if app.JSON {
		return app.emitChecks(checks)
	}

	worst := false
	w := tabwriter.NewWriter(app.Stdout, 0, 0, 2, ' ', 0)
	for _, c := range checks {
		mark := "ok"
		if !c.OK {
			mark = "warn"
			if c.Fatal {
				mark = "FAIL"
				worst = true
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", mark, c.Name, c.Detail)
		if !c.OK && c.Fix != "" {
			fmt.Fprintf(w, "\t\t→ %s\n", c.Fix)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if worst {
		return rlerr.Preconditionf("the panel has problems that stop it working").
			WithHint("each FAIL above names what to run")
	}
	return nil
}

func (app *App) runChecks(ctx context.Context) []check {
	var out []check
	add := func(c check) { out = append(out, c) }

	// The configuration.
	if app.Cfg.Loaded {
		add(check{Name: "config", OK: true, Detail: app.Cfg.SourcePath})
	} else {
		add(check{Name: "config", OK: false, Detail: "no configuration file; running on defaults",
			Fix: "ratline-panel install"})
	}

	// The binary the whole product depends on.
	if _, err := rl.NewClient(app.Cfg.Ratline.Binary, app.Runner, app.Log); err != nil {
		add(check{Name: "ratline", OK: false, Fatal: true,
			Detail: err.Error(), Fix: "install ratline, or correct ratline.binary in " + app.Cfg.SourcePath})
	} else if client, err := rl.NewClient(app.Cfg.Ratline.Binary, app.Runner, app.Log); err == nil {
		cat, cerr := client.Catalogue(ctx)
		if cerr != nil {
			add(check{Name: "ratline", OK: false, Fatal: true, Detail: cerr.Error(),
				Fix: "run '" + app.Cfg.Ratline.Binary + " version' and see what it says"})
		} else {
			add(check{Name: "ratline", OK: true,
				Detail: fmt.Sprintf("%s, %d commands", cat.Version, len(cat.Leaves))})
			if missing := rl.UnclassifiedMutations(cat); len(missing) > 0 {
				add(check{Name: "policy", OK: false,
					Detail: fmt.Sprintf("%d ratline commands have no panel policy and are super-admin only", len(missing)),
					Fix:    "upgrade ratline-panel so it knows about them"})
			} else {
				add(check{Name: "policy", OK: true, Detail: "every command is classified"})
			}
		}
	}

	// The database, and whether anybody has claimed the panel.
	st, err := app.openStore()
	if err != nil {
		add(check{Name: "database", OK: false, Fatal: true, Detail: err.Error()})
	} else {
		defer st.Close() //nolint:errcheck // a read-only command exiting
		version, _ := st.SchemaVersion(ctx)
		add(check{Name: "database", OK: true,
			Detail: fmt.Sprintf("%s, schema %d", app.Cfg.Paths.StateDB, version)})

		if fi, err := os.Stat(app.Cfg.Paths.StateDB); err == nil && fi.Mode().Perm()&0o077 != 0 {
			add(check{Name: "database mode", OK: false,
				Detail: fmt.Sprintf("%s is mode %04o; it holds password hashes and session tokens",
					app.Cfg.Paths.StateDB, fi.Mode().Perm()),
				Fix: "chmod 0600 " + app.Cfg.Paths.StateDB})
		}

		n, _ := st.CountAccounts(ctx)
		switch n {
		case 0:
			// The installer creates one, so an empty database means --no-admin,
			// --purge, or somebody deleting the last account by hand. Whichever it
			// was, the panel is now claimable by whoever reaches it.
			add(check{Name: "accounts", OK: false, Fatal: true,
				Detail: "this panel has no account, so the first visitor becomes its super admin",
				Fix:    "ratline-panel account create you@example.com --role superadmin"})
		default:
			supers := 0
			if accounts, err := st.ListAccounts(ctx); err == nil {
				for _, a := range accounts {
					if a.Role == "superadmin" && !a.Disabled {
						supers++
					}
				}
			}
			if supers == 0 {
				add(check{Name: "accounts", OK: false, Fatal: true,
					Detail: "there is no active super admin, so nobody can invite anyone",
					Fix:    "ratline-panel account role <email> superadmin"})
			} else {
				add(check{Name: "accounts", OK: true,
					Detail: fmt.Sprintf("%d accounts, %d super admins", n, supers)})
			}
		}
	}

	// The interface. A binary built without the bundle serves a page that says so,
	// which is better than a blank screen — but somebody should be told here rather
	// than discovering it in a browser.
	if web.Built() {
		add(check{Name: "interface", OK: true, Detail: "the built interface is in this binary"})
	} else {
		add(check{Name: "interface", OK: false,
			Detail: "this binary carries the placeholder page, not the interface",
			Fix:    "build it: npm --prefix panel/web run build && make panel"})
	}

	// The service.
	if system.Exists(install.UnitPath) {
		add(check{Name: "service", OK: true, Detail: install.UnitPath})
	} else {
		add(check{Name: "service", OK: false,
			Detail: "the systemd unit is not installed, so the panel will not survive a reboot",
			Fix:    "ratline-panel install"})
	}

	// Exposure. The most important line in this output for somebody who has just
	// put the panel on the internet.
	switch {
	case app.Cfg.Listen.Domain != "":
		add(check{Name: "exposure", OK: true,
			Detail: "behind nginx on " + app.Cfg.Listen.Domain})
	case app.Cfg.Listen.Address == "0.0.0.0" || app.Cfg.Listen.Address == "::":
		add(check{Name: "exposure", OK: false,
			Detail: "listening on every interface with no domain and no TLS in front of it",
			Fix:    "ratline-panel domain set <name> --email you@example.com, or bind to 127.0.0.1"})
	default:
		add(check{Name: "exposure", OK: true,
			Detail: "loopback only; reach it through an SSH tunnel"})
	}

	// The second factor, which is the difference between one stolen password and a
	// server.
	if app.Cfg.Security.RequireTOTP {
		add(check{Name: "second factor", OK: true, Detail: "required for every account"})
	} else if app.Cfg.Listen.Domain != "" {
		add(check{Name: "second factor", OK: false,
			Detail: "not required, and the panel is on a public domain",
			Fix:    "set security.require_totp: true in " + app.Cfg.SourcePath})
	} else {
		add(check{Name: "second factor", OK: true,
			Detail: "not required, but the panel is not publicly reachable"})
	}
	return out
}

// emitChecks writes the same envelope shape ratline uses, so a monitoring script
// reading both does not need two parsers.
func (app *App) emitChecks(checks []check) error {
	failed := 0
	for _, c := range checks {
		if !c.OK && c.Fatal {
			failed++
		}
	}
	enc := json.NewEncoder(app.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"ok":      failed == 0,
		"command": "ratline-panel doctor",
		"data":    map[string]any{"checks": checks, "failed": failed},
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing JSON output")
	}
	if failed > 0 {
		return rlerr.Preconditionf("the panel has %d problems that stop it working", failed)
	}
	return nil
}
