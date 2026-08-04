package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// checkKeyFilePermissions wraps sshkey's own check so the diag package has one
// place to call and the wrapper can be stubbed in a test.
func checkKeyFilePermissions(home, keyFile string) []string {
	return sshkey.CheckPermissions(home, keyFile)
}

// effectiveSSHDConfig asks sshd what it will actually do.
//
// `sshd -T` rather than reading the config file, because the file is not the
// configuration: Include directives, Match blocks and a drop-in directory all mean
// the file says one thing and the daemon does another. Every diagnosis of an SSH
// problem that reads the file rather than the effective config is guessing.
func effectiveSSHDConfig(ctx context.Context, env *Env) (map[string]string, error) {
	path, err := env.Bins.Path("sshd")
	if err != nil {
		return nil, err
	}
	res, err := env.Runner.Run(ctx, system.Cmd{Path: path, Args: []string{"-T"}})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(res.Out(), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		// sshd repeats a keyword for a multi-valued setting, so the values are joined
		// rather than the last one winning.
		if prior, seen := out[key]; seen {
			out[key] = prior + " " + value
			continue
		}
		out[key] = value
	}
	return out, nil
}

// NginxChecks diagnose the web server itself, rather than one site through it.
func NginxChecks(env *Env) []Check {
	return []Check{
		{
			ID:    "installed",
			Title: "nginx is installed",
			Run: func(context.Context) Result {
				if !env.Bins.Available("nginx") {
					return Fail("nginx is not installed, so nothing ratline generates is served").
						WithFix("apt-get install nginx")
				}
				return Pass("")
			},
		},
		{
			ID:    "config-valid",
			Title: "nginx accepts its whole configuration",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				if err := env.Nginx.Test(ctx); err != nil {
					// This is the failure that takes every site down at once, which is
					// why it comes before anything about a running process: nginx will
					// also refuse to reload, so a stale-but-working process can mask it.
					return Fail("%s", firstLine(err.Error())).
						WithFix("nginx -t names the failing file and line")
				}
				return Pass("")
			},
		},
		{
			ID:    "running",
			Title: "nginx is running and answering",
			Needs: []string{"config-valid"},
			Run: func(ctx context.Context) Result {
				if err := system.ProbeTCP(ctx, "127.0.0.1:80", env.probeTimeout()); err != nil {
					return Fail("nothing is listening on 127.0.0.1:80").
						WithFix("systemctl status nginx")
				}
				return Pass("listening on :80")
			},
		},
		{
			ID:    "snippets",
			Title: "the shared snippets are in place",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				var missing []string
				for _, name := range []string{"proxy-params.conf", "deny-hidden.conf"} {
					if !system.Exists(filepath.Join(env.Cfg.Paths.NginxSnippets, name)) {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					// Every generated vhost includes these, so a missing one makes the
					// whole configuration invalid — which the check above would already
					// have caught. Naming the file turns "unknown directive" into an
					// actionable answer.
					return Fail("missing from %s: %s", env.Cfg.Paths.NginxSnippets,
						strings.Join(missing, ", ")).
						WithFix("ratline init")
				}
				return Pass("%s", env.Cfg.Paths.NginxSnippets)
			},
		},
		{
			ID:    "acme-webroot",
			Title: "the ACME challenge webroot is writable",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				root := env.Cfg.Paths.ACMEWebroot
				fi, err := os.Stat(root)
				if err != nil {
					// Renewal fails silently weeks later without this, which is the worst
					// possible time to discover it.
					return Fail("%s does not exist, so every HTTP-01 renewal will fail", root).
						WithFix("ratline init").WithTopic("tls")
				}
				if !fi.IsDir() {
					return Fail("%s is not a directory", root).WithFix("ratline init")
				}
				return Pass("%s", root)
			},
		},
		{
			ID:    "orphans",
			Title: "no configuration on disk that state does not know about",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				sites, ok := env.sites(ctx, state.SiteFilter{})
				if !ok {
					return Skip("the site records could not be read")
				}
				known := map[string]bool{}
				for _, s := range sites {
					known[s.Domain+".conf"] = true
				}
				entries, err := os.ReadDir(env.Cfg.Paths.NginxSitesAvailable)
				if err != nil {
					return Skip("%s could not be read", env.Cfg.Paths.NginxSitesAvailable)
				}
				var orphans []string
				for _, e := range entries {
					if e.IsDir() || known[e.Name()] || e.Name() == "default" {
						continue
					}
					// Only ratline's own files. Something a human wrote is not an orphan;
					// it is somebody else's configuration and none of our business.
					path := filepath.Join(env.Cfg.Paths.NginxSitesAvailable, e.Name())
					if managed, _ := system.HasManagedHeader(path); managed {
						orphans = append(orphans, e.Name())
					}
				}
				if len(orphans) > 0 {
					return Warn("%s generated by ratline with no matching site: %s",
						plural(len(orphans), "file"), strings.Join(orphans, ", ")).
						WithFix("ratline reconcile --fix")
				}
				return Pass("%s in state", plural(len(sites), "site"))
			},
		},
	}
}

// SSHChecks diagnose the daemon and ratline's additions to it.
//
// Ordered by what a login depends on, and ending with the lockout guard — because
// the one irrecoverable outcome here is an operator who fixes a warning and can no
// longer reach the server.
func SSHChecks(env *Env) []Check {
	return []Check{
		{
			ID:    "installed",
			Title: "sshd is installed",
			Run: func(context.Context) Result {
				if !env.Bins.Available("sshd") {
					return Fail("sshd is not installed").WithFix("apt-get install openssh-server")
				}
				return Pass("")
			},
		},
		{
			ID:    "config-valid",
			Title: "sshd accepts its configuration",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				path, err := env.Bins.Path("sshd")
				if err != nil {
					return Skip("sshd could not be located")
				}
				// -t rather than restarting: a configuration sshd refuses means the next
				// restart takes the daemon down, and it will keep running happily on the
				// old configuration until then. That gap is where lockouts come from.
				if _, err := env.Runner.Run(ctx, system.Cmd{Path: path, Args: []string{"-t"}}); err != nil {
					return Fail("%s", firstLine(err.Error())).
						WithFix("fix it before the next restart, or sshd will not come back up").
						WithTopic("ssh")
				}
				return Pass("")
			},
		},
		{
			ID:    "listening",
			Title: "sshd is accepting connections",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				eff, err := effectiveSSHDConfig(ctx, env)
				port := "22"
				if err == nil && eff["port"] != "" {
					port = strings.Fields(eff["port"])[0]
				}
				if err := system.ProbeTCP(ctx, "127.0.0.1:"+port, env.probeTimeout()); err != nil {
					return Fail("nothing is listening on port %s", port).
						WithFix("systemctl status ssh")
				}
				return Pass("port %s", port)
			},
		},
		{
			ID:    "pubkey-auth",
			Title: "public-key authentication is enabled",
			Needs: []string{"config-valid"},
			Run: func(ctx context.Context) Result {
				eff, err := effectiveSSHDConfig(ctx, env)
				if err != nil {
					return Skip("the effective configuration could not be read")
				}
				if eff["pubkeyauthentication"] == "no" {
					// Every key on the server is inert. Nothing else here matters.
					return Fail("PubkeyAuthentication is off, so no key on this server authenticates").
						WithFix("this is a manual change under /etc/ssh; ratline never sets it").
						WithTopic("ssh")
				}
				return Pass("")
			},
		},
		{
			ID:    "dropin",
			Title: "ratline's drop-in is installed and included",
			Needs: []string{"config-valid"},
			Run: func(ctx context.Context) Result {
				path := env.Cfg.Paths.SSHDDropIn
				if !system.Exists(path) {
					users, ok := env.users(ctx)
					if ok && len(users) == 0 {
						return Skip("no tenants yet, so there is nothing for it to say")
					}
					return Warn("%s is not installed", path).WithFix("ratline key sync")
				}
				// Present is not the same as in effect: sshd only reads a drop-in if the
				// main config Includes the directory, and that Include is easy to lose
				// when someone replaces sshd_config wholesale.
				eff, err := effectiveSSHDConfig(ctx, env)
				if err != nil {
					return Warn("%s exists, but sshd's effective configuration "+
						"could not be read to confirm it is in force", path)
				}
				if strings.Contains(eff["revokedkeys"], env.Cfg.SSH.RevokedKeys) {
					return Pass("%s, and in force", path)
				}
				return Warn("%s exists but sshd does not appear to be reading it", path).
					WithFix("check for an Include line in /etc/ssh/sshd_config, then " +
						"ratline key sync").WithTopic("ssh")
			},
		},
		{
			ID:    "revoked-list",
			Title: "the revocation list is where sshd expects it",
			Needs: []string{"dropin"},
			Run: func(ctx context.Context) Result {
				path := env.Cfg.SSH.RevokedKeys
				revoked, ok := env.keys(ctx, state.KeyFilter{IncludeRevoked: true})
				if !ok {
					return Skip("the key records could not be read")
				}
				n := 0
				for _, k := range revoked {
					if !k.RevokedAt.IsZero() {
						n++
					}
				}
				if n == 0 {
					return Pass("nothing revoked on this server")
				}
				if !system.Exists(path) {
					// sshd tolerates a missing RevokedKeys file, which is precisely the
					// problem: a revoked key silently works again.
					return Fail("%s are revoked in state but %s does not exist, so sshd "+
						"still accepts them", plural(n, "key"), path).
						WithFix("ratline key sync").WithTopic("ssh")
				}
				return Pass("%s listed", plural(n, "revoked key"))
			},
		},
		{
			ID:    "expiry-support",
			Title: "expiring keys are enforced somehow",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				expiring := 0
				if keys, ok := env.keys(ctx, state.KeyFilter{}); ok {
					for _, k := range keys {
						if !k.ExpiresAt.IsZero() {
							expiring++
						}
					}
				}
				if expiring == 0 {
					return Skip("no key on this server has an expiry")
				}
				if env.Keys != nil && env.Keys.DetectExpirySupport(ctx) {
					return Pass("sshd supports expiry-time=, and a timer prunes as well")
				}
				if env.Cfg.SSH.PruneExpired {
					// The timer is the fallback, so an old sshd is not a failure — but
					// the enforcement is daily rather than immediate, and that matters.
					return Warn("this sshd does not support expiry-time=, so the %s "+
						"with an expiry are enforced by the daily prune timer rather "+
						"than by sshd itself", plural(expiring, "key")).
						WithFix("nothing to do, but an expiry can be up to a day late").
						WithTopic("ssh")
				}
				return Fail("%s have an expiry, but this sshd cannot enforce it and "+
					"pruning is turned off", plural(expiring, "key")).
					WithFix("enable ssh.prune_expired").WithTopic("ssh")
			},
		},
		{
			ID:    "lockout-guard",
			Title: "at least one key can still reach this server",
			Needs: []string{"pubkey-auth"},
			Run: func(ctx context.Context) Result {
				keys, ok := env.keys(ctx, state.KeyFilter{Scope: "global"})
				if !ok {
					return Skip("the key records could not be read")
				}
				live := 0
				for _, k := range keys {
					if k.RevokedAt.IsZero() &&
						(k.ExpiresAt.IsZero() || k.ExpiresAt.After(time.Now())) {
						live++
					}
				}
				if live > 0 {
					return Pass("%s in global scope", plural(live, "live key"))
				}
				eff, _ := effectiveSSHDConfig(ctx, env)
				if eff["passwordauthentication"] == "yes" {
					return Warn("no global-scope key is live; password authentication is " +
						"the only way in").
						WithFix("ratline key add --global --file <pubkey>").WithTopic("ssh")
				}
				// The one genuinely irrecoverable state, and the whole reason this check
				// is last: everything above may be perfect.
				return Fail("no global-scope key is live and password authentication is off — " +
					"a reboot or an sshd restart could leave this server reachable only " +
					"from a console").
					WithFix("add a key now: ratline key add --global --file <pubkey>").
					WithTopic("ssh")
			},
		},
	}
}
