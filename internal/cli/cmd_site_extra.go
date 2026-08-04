package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// newSiteRuntimeCommand changes a site's interpreter version.
func newSiteRuntimeCommand(g *Globals) *cobra.Command {
	var (
		nodeVersion   string
		pythonVersion string
		relax         []string
	)
	cmd := &cobra.Command{
		Use:   "runtime <domain>",
		Short: "Change a site's interpreter version, then rebuild and restart",
		Args:  cobra.ExactArgs(1),
		Example: "  ratline site runtime app.example.com --node 22\n" +
			"  ratline site runtime api.example.com --python 3.12",
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeVersion == "" && pythonVersion == "" && len(relax) == 0 {
				return rlerr.Usagef("nothing to change").
					WithHint("pass --node, --python, or --relax <directive>")
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			switch {
			case nodeVersion != "":
				if site.Runtime != "node" {
					return rlerr.Usagef("%s is a %s site, so --node does not apply", site.Domain, site.Runtime)
				}
				if err := validate.NodeVersion(nodeVersion); err != nil {
					return err
				}
				bin := filepath.Join(g.Cfg.Paths.RuntimesDir, "node", nodeVersion, "bin", "node")
				if !system.Exists(bin) && !g.DryRun {
					return rlerr.Preconditionf("Node %s is not installed", nodeVersion).
						WithHint("ratline runtime install node %s", nodeVersion)
				}
				site.NodeVersion = nodeVersion
			case pythonVersion != "":
				if site.Runtime != "python" {
					return rlerr.Usagef("%s is a %s site, so --python does not apply", site.Domain, site.Runtime)
				}
				if err := validate.PythonVersion(pythonVersion); err != nil {
					return err
				}
				site.PythonVersion = pythonVersion
			}
			if len(relax) > 0 {
				if err := validateRelaxNames(relax); err != nil {
					return err
				}
				site.Relaxed = mergeUnique(site.Relaxed, relax)
			}

			if err := st.PutSite(cmd.Context(), site); err != nil {
				return err
			}
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			id, err := system.LookupIdentity(site.Owner)
			if err != nil && !g.DryRun {
				return err
			}
			rt, err := runtime.For(site.Runtime)
			if err != nil {
				return err
			}
			rc := runtime.NewContext(g.Cfg, g.Log, g.Runner, site, id, g.DryRun)

			// A Python venv is built against one interpreter and stops working when
			// that interpreter is replaced, so it has to be rebuilt rather than
			// reused. Node has no equivalent, but native modules are compiled
			// against the ABI, so its dependencies are reinstalled too.
			if site.Runtime == "python" {
				g.Log.Info("rebuilding the virtualenv against the new interpreter")
				if err := rt.Teardown(cmd.Context(), rc); err != nil {
					return err
				}
			}
			if err := rt.Provision(cmd.Context(), rc); err != nil {
				return err
			}
			if err := rt.Install(cmd.Context(), rc); err != nil {
				return err
			}
			if err := rt.Build(cmd.Context(), rc); err != nil {
				return err
			}
			health, err := mgr.Control(cmd.Context(), site.Domain, "restart")
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"site": site, "health": health})
			}
			g.Printf("%s now runs on %s\n", site.Domain, versionOf(site))
			return g.Fields([2]string{"health", health})
		},
	}
	f := cmd.Flags()
	f.StringVar(&nodeVersion, "node", "", "Node version to move to")
	f.StringVar(&pythonVersion, "python", "", "Python version to move to")
	f.StringSliceVar(&relax, "relax", nil, "Turn off a named systemd hardening directive for this site")
	return Mutating(cmd)
}

func versionOf(s *state.Site) string {
	if s.Runtime == "node" {
		return "Node " + orDash(s.NodeVersion)
	}
	return "Python " + orDash(s.PythonVersion)
}

func mergeUnique(existing, added []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(added))
	for _, v := range append(existing, added...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// newSiteDeployKeyCommand manages the outbound keypair a site uses to pull from a
// private repository.
//
// This is the opposite direction from `ratline key add`: that grants someone
// access *to* the server, this grants the server access *to* a repository.
func newSiteDeployKeyCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy-key",
		Short: "Manage the outbound key a site uses to clone a private repository",
		Long: "The private half never leaves this server: it is 0600, owned by the site user,\n" +
			"and used only by git over SSH. The public half is printed for you to paste into\n" +
			"the repository's deploy keys.",
	}
	cmd.AddCommand(
		newDeployKeyCreateCommand(g, false),
		newDeployKeyShowCommand(g),
		newDeployKeyCreateCommand(g, true),
		newDeployKeyRemoveCommand(g),
	)
	return cmd
}

// deployKeyPaths returns where a site's outbound key lives.
func (g *Globals) deployKeyPaths(site *state.Site) (dir, priv, pub, config string) {
	dir = filepath.Join(g.Cfg.SiteDir(site.Owner, site.Domain), ".ssh")
	return dir, filepath.Join(dir, "deploy_key"), filepath.Join(dir, "deploy_key.pub"),
		filepath.Join(dir, "config")
}

func newDeployKeyCreateCommand(g *Globals, rotate bool) *cobra.Command {
	verb, short := "create", "Generate an outbound keypair for a site"
	if rotate {
		verb, short = "rotate", "Replace a site's outbound keypair"
	}
	var keyType string
	cmd := &cobra.Command{
		Use:   verb + " <domain>",
		Short: short,
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
			dir, priv, pub, sshConfig := g.deployKeyPaths(site)

			if system.Exists(priv) && !rotate {
				return rlerr.Preconditionf("%s already has a deploy key", site.Domain).
					WithHint("show it with 'ratline site deploy-key show %s', or replace it with 'rotate'", site.Domain)
			}
			if g.DryRun {
				g.Log.Info("would generate a deploy key", "path", priv, "type", keyType)
				return nil
			}

			id, err := system.LookupIdentity(site.Owner)
			if err != nil {
				return err
			}
			if _, err := system.EnsureDir(dir, 0o700, id.UID, id.GID); err != nil {
				return err
			}
			if rotate {
				// Removed first: ssh-keygen refuses to overwrite non-interactively,
				// and the old key is worthless the moment the new one is registered.
				for _, p := range []string{priv, pub} {
					if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
						return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", p)
					}
				}
			}

			// Generated as the site user so the private key is never briefly owned
			// by root in a directory the tenant can read.
			if _, err := g.Runner.Run(cmd.Context(), system.Cmd{
				Name: "ssh-keygen",
				Args: []string{"-t", keyType, "-N", "", "-C",
					"ratline-deploy-" + site.Domain, "-f", priv},
				As: id, Dir: dir, Mutates: true, Label: "ssh-keygen",
			}); err != nil {
				return err
			}
			if err := system.Chmod(priv, 0o600); err != nil {
				return err
			}
			if err := system.Chmod(pub, 0o644); err != nil {
				return err
			}

			// An ssh config entry, so `git clone` uses this key without the tenant
			// having to remember GIT_SSH_COMMAND.
			cfg := "# " + system.ManagedHeader + "\n" +
				"# Used by git when cloning or pulling for " + site.Domain + ".\n" +
				"Host *\n" +
				"    IdentityFile " + priv + "\n" +
				"    IdentitiesOnly yes\n" +
				"    StrictHostKeyChecking accept-new\n"
			if err := system.WriteFileAtomic(sshConfig, []byte(cfg), 0o600, id.UID, id.GID); err != nil {
				return err
			}

			pubKey, err := system.ReadFileLimit(pub, 1<<16)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain,
					"public_key": strings.TrimSpace(string(pubKey)), "path": priv})
			}
			g.Printf("Generated a deploy key for %s.\n\nAdd this public key to the repository:\n\n%s\n",
				site.Domain, strings.TrimSpace(string(pubKey)))
			g.Printf("\n  GitHub:  Settings → Deploy keys → Add deploy key\n" +
				"  GitLab:  Settings → Repository → Deploy keys\n\n" +
				"Leave write access off unless the site needs to push.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&keyType, "type", "ed25519", "Key type: ed25519 or rsa")
	return Mutating(cmd)
}

func newDeployKeyShowCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show <domain>",
		Short: "Print a site's outbound public key",
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
			_, priv, pub, _ := g.deployKeyPaths(site)
			if !system.Exists(pub) {
				return rlerr.Preconditionf("%s has no deploy key", site.Domain).
					WithHint("create one: ratline site deploy-key create %s", site.Domain)
			}
			data, err := system.ReadFileLimit(pub, 1<<16)
			if err != nil {
				return err
			}
			// Only ever the public half. The private key is never printed, logged or
			// included in JSON output.
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain,
					"public_key": strings.TrimSpace(string(data)), "private_key_path": priv})
			}
			g.Println(strings.TrimSpace(string(data)))
			return nil
		},
	}
}

func newDeployKeyRemoveCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <domain>",
		Aliases: []string{"rm"},
		Short:   "Delete a site's outbound keypair",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, priv, pub, sshConfig := g.deployKeyPaths(site)
			if g.DryRun {
				g.Log.Info("would remove the deploy key", "path", priv)
				return nil
			}
			removed := 0
			for _, p := range []string{priv, pub, sshConfig} {
				if err := os.Remove(p); err == nil {
					removed++
				} else if !os.IsNotExist(err) {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", p)
				}
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": site.Domain, "files_removed": removed})
			}
			g.Printf("Removed the deploy key for %s. Remember to delete it from the repository too.\n", site.Domain)
			return nil
		},
	}
	return Mutating(cmd)
}

// newBackupCommand archives a user or a site.
func newBackupCommand(g *Globals) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:     "backup <user|domain>",
		Short:   "Archive a user's home or a single site",
		GroupID: GroupOps,
		Args:    cobra.ExactArgs(1),
		Long: "Writes a gzipped tar of the tenant's home or the site directory, including the\n" +
			"application code, the logs and the .env.\n\n" +
			"The archive therefore contains secrets. It is written 0600 in a 0700 directory,\n" +
			"and where it goes afterwards is your responsibility.",
		Example: "  ratline backup acme --out /var/backups/ratline\n" +
			"  ratline backup example.com --out /mnt/backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				out = g.Cfg.Paths.BackupDir
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			target := args[0]

			var source, label string
			if site, err := st.FindSiteByName(cmd.Context(), target); err == nil {
				source = g.Cfg.SiteDir(site.Owner, site.Domain)
				label = site.Domain
			} else if u, err := st.GetUser(cmd.Context(), target); err == nil {
				source = u.Home
				label = u.Name
			} else {
				return rlerr.Preconditionf("no user or site called %s", target).
					WithHint("list them with 'ratline user list' and 'ratline site list'")
			}
			if !system.Exists(source) {
				return rlerr.Preconditionf("%s does not exist", source)
			}

			if _, err := system.EnsureDir(out, 0o700, 0, 0); err != nil {
				return err
			}
			stamp := time.Now().UTC().Format("20060102T150405Z")
			archive := filepath.Join(out, fmt.Sprintf("%s-%s.tar.gz", label, stamp))
			if g.DryRun {
				g.Log.Info("would archive", "from", source, "to", archive)
				return nil
			}

			size, _ := system.DirSize(source)
			g.Log.Info("archiving", "source", source, "size", validate.FormatSize(size), "archive", archive)
			// -C with a relative path keeps absolute paths out of the archive, so it
			// can be restored anywhere rather than only over the original location.
			if _, err := g.Runner.Run(cmd.Context(), system.Cmd{
				Name: "tar",
				Args: []string{"--create", "--gzip", "--file", archive,
					"-C", filepath.Dir(source), filepath.Base(source)},
				Mutates: true, Timeout: 2 * time.Hour, Label: "tar",
			}); err != nil {
				_ = os.Remove(archive)
				return err
			}
			// The archive holds a tenant's .env, so it is no more readable than the
			// original.
			if err := system.Chmod(archive, 0o600); err != nil {
				return err
			}
			fi, err := os.Stat(archive)
			if err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "checking the archive")
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{"target": label, "archive": archive, "bytes": fi.Size()})
			}
			g.Printf("Archived %s\n", label)
			return g.Fields(
				[2]string{"archive", archive},
				[2]string{"size", validate.FormatSize(fi.Size())},
				[2]string{"note", "contains .env and application data — treat it as a secret"},
			)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Directory to write the archive into")
	return Mutating(cmd)
}

// validateRelaxNames refuses a directive name that is not one ratline sets, so a
// typo does not silently do nothing.
func validateRelaxNames(names []string) error {
	known := map[string]bool{}
	var all []string
	for _, h := range hardeningNames() {
		known[h] = true
		all = append(all, h)
	}
	for _, n := range names {
		if !known[n] {
			return rlerr.Usagef("unknown hardening directive %q", n).
				WithHint("one of: %s", strings.Join(all, ", "))
		}
	}
	return nil
}
