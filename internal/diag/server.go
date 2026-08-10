package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/mongod"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// ServerChecks diagnose the host and the things every other diagnosis assumes.
//
// This is the default subject because it is the right first question on a server
// where something is wrong and you do not yet know what: a broken clock, a full
// disk or a missing binary explains a dozen downstream symptoms, and diagnosing any
// one of those symptoms first is wasted work.
//
// It ends by naming the resources that are unhealthy rather than diagnosing each
// one, so the output stays one screen and says where to go next.
func ServerChecks(env *Env) []Check {
	return []Check{
		{
			ID:    "config",
			Title: "the configuration file is loaded",
			Run: func(context.Context) Result {
				if !env.Cfg.Loaded {
					return Warn("no configuration at %s, so the built-in defaults are in use",
						env.Cfg.SourcePath).WithFix("ratline init")
				}
				return Pass("%s", env.Cfg.SourcePath)
			},
		},
		{
			ID:    "platform",
			Title: "the host is one ratline targets",
			Run: func(context.Context) Result {
				if !env.OS.Supported() {
					return Warn("%s is not Debian or Ubuntu, so the filesystem layout "+
						"may differ from what ratline assumes", env.OS.PrettyName)
				}
				return Pass("%s", env.OS.PrettyName)
			},
		},
		{
			ID:    "clock",
			Title: "the clock is plausible",
			Run: func(context.Context) Result {
				// A skewed clock breaks TLS validation, ACME issuance and every
				// expiry comparison in this tool, and each of those failures looks like
				// something else. Cheap to check, and it explains a lot when wrong.
				res, err := env.Runner.Run(context.Background(), system.Cmd{
					Name: "timedatectl", Args: []string{"show", "--property=NTPSynchronized", "--value"},
				})
				if err != nil {
					return Skip("timedatectl is unavailable")
				}
				if strings.TrimSpace(res.Out()) == "no" {
					return Warn("the clock is not synchronised, which breaks certificate " +
						"validity comparisons and ACME issuance").
						WithFix("timedatectl set-ntp true").WithTopic("tls")
				}
				return Pass("synchronised")
			},
		},
		{
			ID:    "tooling",
			Title: "the binaries ratline drives are installed",
			Run: func(context.Context) Result {
				var missing []string
				for _, bin := range []string{"nginx", "systemctl", "ssh-keygen"} {
					if !env.Bins.Available(bin) {
						missing = append(missing, bin)
					}
				}
				if len(missing) > 0 {
					return Fail("not installed: %s", strings.Join(missing, ", ")).
						WithFix("apt-get install %s", strings.Join(missing, " "))
				}
				if !env.Bins.Available("certbot") {
					return Warn("certbot is not installed, so certificates cannot be issued").
						WithFix("apt-get install certbot")
				}
				return Pass("")
			},
		},
		{
			ID:    "disk",
			Title: "there is disk space left",
			Run: func(context.Context) Result {
				// A full disk is the cause that produces the widest spread of unrelated
				// symptoms: failed writes, failed renewals, failed deploys, a corrupt
				// state database. Worth its own line rather than being inferred.
				for _, path := range []string{env.Cfg.Paths.HomeBase, filepath.Dir(env.Cfg.Paths.StateDB)} {
					free, total, err := diskFree(path)
					if err != nil || total == 0 {
						continue
					}
					pct := free * 100 / total
					if pct < 5 {
						return Fail("%s has %d%% free, which is where writes start failing", path, pct).
							WithFix("free space before doing anything else")
					}
					if pct < 15 {
						return Warn("%s has %d%% free", path, pct)
					}
				}
				return Pass("")
			},
		},
		{
			ID:    "state",
			Title: "the state database is readable and private",
			Run: func(ctx context.Context) Result {
				path := env.Cfg.Paths.StateDB
				fi, err := os.Stat(path)
				if err != nil {
					return Fail("%s is missing", path).WithFix("ratline init")
				}
				if fi.Mode().Perm()&0o077 != 0 {
					// It holds the map of who owns what. A tenant has no reason to read it.
					return Fail("%s is mode %04o and should be 0600", path, fi.Mode().Perm()).
						WithFix("chmod 0600 %s", path).WithTopic("state")
				}
				v, ok := env.schemaVersion(ctx)
				if !ok {
					return Fail("the schema version could not be read").
						WithFix("ratline reconcile, or restore from a backup").WithTopic("state")
				}
				return Pass("schema %d, mode %04o", v, fi.Mode().Perm())
			},
		},
		{
			ID:    "audit",
			Title: "the audit log is writable",
			Needs: []string{"state"},
			Run: func(context.Context) Result {
				path := env.Cfg.Paths.AuditLog
				if !system.Exists(path) {
					// Losing the audit trail never stops an operator managing the server,
					// so this is a warning by design rather than a failure.
					return Warn("%s does not exist yet, so mutations are not being recorded",
						path).WithFix("ratline init").WithTopic("state")
				}
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o640)
				if err != nil {
					return Warn("%s cannot be written: %s", path, firstLine(err.Error())).
						WithFix("check the ownership of %s", filepath.Dir(path))
				}
				f.Close()
				return Pass("%s", path)
			},
		},
		{
			ID:    "lock",
			Title: "no stale mutation lock is held",
			Run: func(context.Context) Result {
				// A held lock is normal while another invocation runs. A lock file with
				// no live holder is what a killed process leaves behind, and it blocks
				// every mutation until removed.
				path := env.Cfg.Paths.Lock
				if !system.Exists(path) {
					return Pass("")
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return Pass("")
				}
				if holder := strings.TrimSpace(string(body)); holder != "" {
					return Warn("%s is held by %s", path, holder).
						WithFix("if no ratline command is running, remove %s", path)
				}
				return Pass("")
			},
		},
		{
			ID:    "nginx",
			Title: "the web server is healthy",
			Needs: []string{"tooling"},
			Run: func(ctx context.Context) Result {
				sub := Run(ctx, "nginx", "", "", NginxChecks(env))
				if !sub.Healthy() {
					return Fail("%s", sub.Cause).WithFix("ratline troubleshoot nginx")
				}
				if sub.Warnings > 0 {
					return Warn("%s worth looking at", plural(sub.Warnings, "warning")).
						WithFix("ratline troubleshoot nginx")
				}
				return Pass("")
			},
		},
		{
			ID:    "ssh",
			Title: "SSH access is healthy",
			Run: func(ctx context.Context) Result {
				sub := Run(ctx, "ssh", "", "", SSHChecks(env))
				if !sub.Healthy() {
					return Fail("%s", sub.Cause).WithFix("ratline troubleshoot ssh")
				}
				if sub.Warnings > 0 {
					return Warn("%s worth looking at", plural(sub.Warnings, "warning")).
						WithFix("ratline troubleshoot ssh")
				}
				return Pass("")
			},
		},
		{
			ID:    "mongodb-exposure",
			Title: "mongod is not exposed without a firewall standing guard",
			Run: func(ctx context.Context) Result {
				// Asked of the socket and the firewall, not the config file: the
				// finding that matters is who can actually connect. One shared
				// implementation with the bare `doctor` sweep, so the two cannot
				// drift the way walk-only fixes have here before.
				mgr := &mongod.Manager{
					Cfg: env.Cfg, Log: env.Log, Runner: env.Runner,
					Bins: env.Bins, State: env.State, OS: env.OS,
				}
				exp, err := mgr.CheckExposure(ctx)
				if err != nil {
					return Warn("could not determine what mongod is bound to: %v", err)
				}
				switch {
				case !exp.Present:
					return Skip("no MongoDB server on this host")
				case !exp.Remote:
					return Pass("mongod listens on localhost only")
				case exp.Guarded:
					return Pass("mongod listens beyond localhost; ufw admits %s",
						plural(exp.Allowed, "allowed address"))
				default:
					return Fail("mongod listens beyond localhost and no firewall is standing " +
						"guard, so anyone who can reach port " + mongod.Port + " faces only a password").
						WithFix("activate ufw with a default-deny incoming policy (allow SSH first), " +
							"or revoke every address: ratline db access list")
				}
			},
		},
		{
			ID:    "tenants",
			Title: "every tenant in state has an account",
			Needs: []string{"state"},
			Run: func(ctx context.Context) Result {
				users, ok := env.users(ctx)
				if !ok {
					return Skip("the tenant records could not be read")
				}
				var missing []string
				for _, u := range users {
					if !system.UserExists(u.Name) {
						missing = append(missing, u.Name)
					}
				}
				if len(missing) > 0 {
					return Fail("recorded in state with no system account: %s",
						strings.Join(missing, ", ")).
						WithFix("ratline troubleshoot %s", missing[0])
				}
				return Pass("%s", plural(len(users), "tenant"))
			},
		},
		{
			ID:    "sites",
			Title: "every enabled site is serving",
			Needs: []string{"tenants"},
			Run: func(ctx context.Context) Result {
				sites, ok := env.sites(ctx, state.SiteFilter{})
				if !ok {
					return Skip("the site records could not be read")
				}
				var broken []string
				for _, s := range sites {
					if !s.Enabled || !s.Dynamic() {
						continue
					}
					st, ok := env.unitStatus(ctx, s)
					if !ok || st.Active != "active" {
						broken = append(broken, s.Domain)
					}
				}
				if len(broken) > 0 {
					// Named rather than diagnosed: each one has its own request-path walk,
					// and running three of them inline would bury this answer.
					return Fail("not running: %s", strings.Join(broken, ", ")).
						WithFix("ratline troubleshoot %s", broken[0])
				}
				return Pass("%s", plural(len(sites), "site"))
			},
		},
		{
			ID:    "certificates",
			Title: "no certificate is expired or failing to renew",
			Needs: []string{"state"},
			Run: func(ctx context.Context) Result {
				if env.TLS == nil {
					return Skip("the certificate inventory is unavailable")
				}
				rows, err := env.TLS.List(ctx, 0, false)
				if err != nil {
					return Skip("the certificate inventory could not be read")
				}
				var expired, failing, expiring []string
				for _, r := range rows {
					switch {
					case r.Days <= 0:
						expired = append(expired, r.Name)
					case r.ConsecutiveFailures > 0:
						failing = append(failing, r.Name)
					case r.Days < env.Cfg.ACME.RenewBeforeDays:
						expiring = append(expiring, r.Name)
					}
				}
				if len(expired) > 0 {
					return Fail("expired: %s", strings.Join(expired, ", ")).
						WithFix("ratline troubleshoot %s", expired[0]).WithTopic("tls")
				}
				if len(failing) > 0 {
					return Fail("renewal is failing: %s", strings.Join(failing, ", ")).
						WithFix("ratline troubleshoot %s", failing[0]).WithTopic("tls")
				}
				if len(expiring) > 0 {
					return Warn("inside the renewal window: %s", strings.Join(expiring, ", "))
				}
				return Pass("%s", plural(len(rows), "certificate"))
			},
		},
		{
			ID:    "acme-trust",
			Title: "every certificate can verify the CA it renews from",
			Needs: []string{"certificates"},
			Run: func(ctx context.Context) Result {
				// The certificates check above only fires once renewals have already
				// failed, because it reads the failure counter. This one is about the
				// cause, and it can be seen the day the server is set up: certbot
				// verifies an ACME directory against certifi's bundled roots rather than
				// the system trust store, so a private CA needs acme.ca_bundle, and
				// without it every renewal fails months later for no visible reason.
				if env.TLS == nil {
					return Skip("the certificate inventory is unavailable")
				}
				rows, err := env.TLS.List(ctx, 0, false)
				if err != nil {
					return Skip("the certificate inventory could not be read")
				}
				var private []string
				for _, r := range rows {
					if server := renewalServerFor(env, r.Name); server != "" && !isPublicACME(server) {
						private = append(private, r.Name)
					}
				}
				if len(private) == 0 {
					return Pass("all from a public CA")
				}
				bundle := acmeCABundle(env)
				if bundle == "" {
					return Fail("%s renew from a private CA and acme.ca_bundle is not set",
						strings.Join(private, ", ")).
						WithFix("set acme.ca_bundle in /etc/ratline/config.yaml to that CA's root; "+
							"'ratline cert renew %s --dry-run' shows it failing today", private[0]).
						WithTopic("tls")
				}
				if !exists(bundle) {
					return Fail("acme.ca_bundle points at %s, which does not exist", bundle).
						WithFix("correct acme.ca_bundle in /etc/ratline/config.yaml").
						WithTopic("tls")
				}
				return Pass("%s verified with %s", plural(len(private), "private-CA certificate"), bundle)
			},
		},
		{
			ID:    "drift",
			Title: "the filesystem matches what state describes",
			Needs: []string{"state"},
			Run: func(ctx context.Context) Result {
				sites, ok := env.sites(ctx, state.SiteFilter{})
				if !ok {
					return Skip("the site records could not be read")
				}
				var drifted []string
				for _, s := range sites {
					if !system.Exists(env.Cfg.VhostPath(s.Domain)) {
						drifted = append(drifted, s.Domain+" (no vhost)")
						continue
					}
					if s.Enabled && !system.IsSymlink(env.Cfg.VhostLink(s.Domain)) {
						drifted = append(drifted, s.Domain+" (not linked)")
					}
				}
				if len(drifted) > 0 {
					return Fail("%s", strings.Join(drifted, ", ")).
						WithFix("ratline reconcile --fix")
				}
				return Pass("")
			},
		},
	}
}

// diskFree returns free and total bytes for the filesystem holding path.
func diskFree(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	return st.Bavail * bs, st.Blocks * bs, nil
}
