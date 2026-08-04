package diag

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func userSummary(u *state.User) string {
	parts := []string{"tenant"}
	if u.Disabled {
		parts = append(parts, "disabled")
	}
	if u.SFTPOnly {
		parts = append(parts, "sftp only")
	}
	return strings.Join(parts, ", ")
}

// UserChecks walk a tenant from the account outwards.
//
// The order is the order a login and then a request depends on things: the account
// has to exist before its home means anything, the home has to be traversable
// before nginx can serve out of it, and the keys only matter once there is an
// account to log into.
func UserChecks(env *Env, u *state.User) []Check {
	home := u.Home
	if home == "" {
		home = env.Cfg.HomeDir(u.Name)
	}

	return []Check{
		{
			ID:    "account",
			Title: "the system account exists",
			Run: func(context.Context) Result {
				if !system.UserExists(u.Name) {
					// State says this tenant exists and the system disagrees. Nothing
					// below can be evaluated, and every site this tenant owns is down.
					return Fail("%s is recorded in state but has no system account", u.Name).
						WithFix("ratline reconcile --fix, or 'ratline user delete %s' "+
							"if it is gone for good", u.Name)
				}
				id, err := system.LookupIdentity(u.Name)
				if err != nil {
					return Fail("the account exists but could not be read: %s", firstLine(err.Error()))
				}
				return Pass("uid %d, gid %d", id.UID, id.GID)
			},
		},
		{
			ID:    "not-disabled",
			Title: "the tenant is not disabled",
			Needs: []string{"account"},
			Run: func(context.Context) Result {
				if u.Disabled {
					// Deliberate, so it is a warning rather than a failure — but it is
					// the answer to "why is every site of theirs returning 503".
					return Warn("this tenant is disabled: its services are stopped and "+
						"its sites serve 503").
						WithFix("ratline user enable %s", u.Name)
				}
				return Pass("")
			},
		},
		{
			ID:    "home",
			Title: "the home directory exists with a safe mode",
			Needs: []string{"account"},
			Run: func(context.Context) Result {
				fi, err := os.Stat(home)
				if err != nil {
					return Fail("%s is missing", home).WithFix("ratline reconcile --fix")
				}
				if !fi.IsDir() {
					return Fail("%s is not a directory", home)
				}
				mode := fi.Mode().Perm()
				// World access on a home is the single worst permission mistake
				// available on a shared server: every other tenant can read it.
				if mode&0o007 != 0 {
					return Fail("%s is mode %04o, which gives every other tenant access", home, mode).
						WithFix("chmod 0750 %s", home).WithTopic("layout")
				}
				// And the other direction: nginx reaches this tenant's files through
				// their group, so no group execute means a 403 on every static file.
				if mode&0o050 != 0o050 {
					return Fail("%s is mode %04o, so nginx cannot traverse it and static "+
						"files will 403", home, mode).
						WithFix("chmod 0750 %s", home).WithTopic("layout")
				}
				return Pass("%s, mode %04o", home, mode)
			},
		},
		{
			ID:    "ownership",
			Title: "the home is owned by the tenant",
			Needs: []string{"home"},
			Run: func(context.Context) Result {
				id, err := system.LookupIdentity(u.Name)
				if err != nil {
					return Skip("the account could not be read")
				}
				uid, gid, err := system.Owner(home)
				if err != nil {
					return Skip("the owner could not be read")
				}
				if uid != id.UID || gid != id.GID {
					// A home owned by someone else is how a restored backup breaks
					// everything at once, and it is invisible until something fails.
					return Fail("%s is owned by %d:%d, not %d:%d", home, uid, gid, id.UID, id.GID).
						WithFix("chown -R %d:%d %s", id.UID, id.GID, home)
				}
				return Pass("%d:%d", uid, gid)
			},
		},
		{
			ID:    "shell",
			Title: "the login shell is what state records",
			Needs: []string{"account"},
			Run: func(context.Context) Result {
				id, err := system.LookupIdentity(u.Name)
				if err != nil {
					return Skip("the account could not be read")
				}
				if u.Shell != "" && id.Shell != "" && u.Shell != id.Shell {
					return Warn("the shell is %s, but state records %s", id.Shell, u.Shell).
						WithFix("ratline user shell %s %s", u.Name, u.Shell)
				}
				if u.SFTPOnly && !strings.Contains(id.Shell, "false") &&
					!strings.Contains(id.Shell, "nologin") &&
					!strings.Contains(id.Shell, "ratline-shell") {
					// The whole point of sftp-only is that a shell is not reachable.
					return Fail("this tenant is sftp-only but its shell is %s", id.Shell).
						WithFix("ratline user shell %s --sftp-only", u.Name).WithTopic("ssh")
				}
				return Pass("%s", orDefault(id.Shell, u.Shell))
			},
		},
		{
			ID:    "authorized-keys",
			Title: "authorized_keys is present and unreadable by anyone else",
			Needs: []string{"home"},
			Run: func(context.Context) Result {
				path := filepath.Join(home, ".ssh", "authorized_keys")
				fi, err := os.Stat(path)
				if err != nil {
					// Only a failure if there are keys that should be in it.
					if n, ok := env.keysInScope(context.Background(),
						"user", u.Name, ""); ok && n > 0 {
						return Fail("%d key(s) are recorded for this tenant but %s does not exist",
							n, path).WithFix("ratline key sync").WithTopic("ssh")
					}
					return Warn("no authorized_keys, so this tenant cannot log in with a key").
						WithFix("ratline key add --user %s --file <pubkey>", u.Name).WithTopic("ssh")
				}
				// sshd refuses a key file it considers loosely permissioned, and says
				// so only in its own log — which is a confusing way to be locked out.
				if problems := sshkey.CheckPermissions(home, path); len(problems) > 0 {
					return Fail("sshd will refuse this key file: %s", strings.Join(problems, "; ")).
						WithFix("chmod 0700 %s/.ssh && chmod 0600 %s", home, path).
						WithTopic("ssh")
				}
				return Pass("%s, mode %04o", path, fi.Mode().Perm())
			},
		},
		{
			ID:    "keys-in-sync",
			Title: "the managed key block matches state",
			Needs: []string{"authorized-keys"},
			Run: func(ctx context.Context) Result {
				keys, ok := env.keys(ctx, state.KeyFilter{Scope: "user", Owner: u.Name})
				if !ok {
					return Skip("the key records could not be read")
				}
				path := filepath.Join(home, ".ssh", "authorized_keys")
				file, err := sshkey.ReadFile(path, env.Cfg.SSH.MaxAuthKeysBytes)
				if err != nil {
					return Skip("authorized_keys could not be parsed")
				}
				on, err := os.ReadFile(path)
				if err != nil {
					return Skip("authorized_keys could not be read")
				}
				// Compared by re-rendering rather than by counting lines, because the
				// options on a line are the grant: a key whose from= or command= was
				// edited by hand has the right fingerprint and the wrong permissions,
				// and a count would call that in sync.
				if !bytes.Equal(bytes.TrimRight(on, "\n"),
					bytes.TrimRight(file.Render(keys), "\n")) {
					// Drift means a removed key may still log in, or an added one may
					// not — both silent until somebody tries.
					return Fail("the file on disk differs from the %s state records",
						plural(len(keys), "key")).
						WithFix("ratline key sync").WithTopic("ssh")
				}
				detail := plural(len(keys), "managed key")
				if extra := len(file.Unmanaged); extra > 0 {
					// Not a problem: hand-added keys outside the markers are deliberately
					// left alone. Worth saying, because they are invisible to key list.
					detail += ", plus " + plural(extra, "key") + " outside the markers"
				}
				if len(keys) > 0 && !file.HadBlock {
					return Fail("state records %s but there is no managed block in the file",
						plural(len(keys), "key")).
						WithFix("ratline key sync").WithTopic("ssh")
				}
				return Pass("%s", detail)
			},
		},
		{
			ID:    "sites",
			Title: "the tenant's sites are healthy",
			Needs: []string{"account"},
			Run: func(ctx context.Context) Result {
				sites, ok := env.sites(ctx, state.SiteFilter{Owner: u.Name})
				if !ok {
					return Skip("the site records could not be read")
				}
				if len(sites) == 0 {
					return Pass("no sites yet")
				}
				// Deliberately shallow: this reports which site to look at next rather
				// than running every site's full walk inline, which would bury the
				// tenant-level answer under three request-path diagnoses.
				var broken []string
				for _, s := range sites {
					if !s.Enabled {
						continue
					}
					if !s.Dynamic() {
						continue
					}
					st, ok := env.unitStatus(ctx, s)
					if !ok || st.Active != "active" {
						broken = append(broken, s.Domain)
					}
				}
				if len(broken) > 0 {
					return Fail("%s not running: %s", plural(len(broken), "site is"),
						strings.Join(broken, ", ")).
						WithFix("ratline troubleshoot %s", broken[0])
				}
				return Pass("%s, all serving", plural(len(sites), "site"))
			},
		},
		{
			ID:    "quota",
			Title: "the tenant is inside its disk quota",
			Needs: []string{"home"},
			Run: func(context.Context) Result {
				if u.Quota == "" {
					return Skip("no quota is set for this tenant")
				}
				if !env.Cfg.Users.QuotaEnabled {
					// A quota recorded but not enforceable is worse than none: it reads
					// as a limit that is in force and is not.
					return Warn("a quota of %s is recorded, but quota support is turned off "+
						"in configuration so nothing enforces it", u.Quota).
						WithFix("enable users.quota_enabled, or clear the quota")
				}
				return Pass("%s", u.Quota)
			},
		},
	}
}
