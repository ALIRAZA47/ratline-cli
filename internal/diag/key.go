package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func keySummary(k *state.Key) string {
	parts := []string{k.Algorithm, k.Scope + " scope"}
	if k.Label != "" {
		parts = append([]string{k.Label}, parts...)
	}
	return strings.Join(parts, ", ")
}

// KeyChecks answer one question: can this key log in, and to what.
//
// It is the diagnosis that is hardest to do by hand, because the answer is spread
// across the state record, a file in a home directory, sshd's effective
// configuration and a revocation list — and getting any one of them wrong produces
// a "Permission denied (publickey)" that says nothing about which.
func KeyChecks(env *Env, k *state.Key) []Check {
	// Where this key's line should be, which depends on its scope.
	target := func() (path, home string) {
		switch k.Scope {
		case "global":
			admin := env.Cfg.Server.AdminUser
			if admin == "" {
				return env.Cfg.SSH.GlobalKeysFile, ""
			}
			h := env.Cfg.HomeDir(admin)
			return filepath.Join(h, ".ssh", "authorized_keys"), h
		case "site":
			site, ok := env.site(context.Background(), k.Site)
			if !ok {
				return "", ""
			}
			h := env.Cfg.HomeDir(site.Owner)
			return filepath.Join(h, ".ssh", "authorized_keys"), h
		default:
			h := env.Cfg.HomeDir(k.Owner)
			return filepath.Join(h, ".ssh", "authorized_keys"), h
		}
	}

	return []Check{
		{
			ID:    "not-revoked",
			Title: "the key has not been revoked",
			Run: func(context.Context) Result {
				if !k.RevokedAt.IsZero() {
					// Revocation is deliberate and terminal: sshd consults the revocation
					// list regardless of any authorized_keys file, so nothing below can
					// make this key work again.
					return Fail("revoked on %s, and sshd refuses it regardless of any "+
						"authorized_keys file", k.RevokedAt.UTC().Format(time.DateOnly)).
						WithFix("this key cannot be un-revoked; add a new one instead").
						WithTopic("ssh")
				}
				return Pass("")
			},
		},
		{
			ID:    "not-expired",
			Title: "the key has not expired",
			Needs: []string{"not-revoked"},
			Run: func(context.Context) Result {
				if k.ExpiresAt.IsZero() {
					return Pass("no expiry set")
				}
				left := time.Until(k.ExpiresAt)
				if left <= 0 {
					return Fail("expired on %s", k.ExpiresAt.UTC().Format(time.DateOnly)).
						WithFix("ratline key remove %s, then add a replacement", k.Fingerprint).
						WithTopic("ssh")
				}
				if left < 7*24*time.Hour {
					return Warn("expires in %s, on %s", plural(int(left.Hours()/24), "day"),
						k.ExpiresAt.UTC().Format(time.DateOnly)).
						WithFix("add the replacement before this one lapses")
				}
				return Pass("expires %s", k.ExpiresAt.UTC().Format(time.DateOnly))
			},
		},
		{
			ID:    "scope-target",
			Title: "the account or site this key is scoped to exists",
			Needs: []string{"not-revoked"},
			Run: func(ctx context.Context) Result {
				switch k.Scope {
				case "global":
					admin := env.Cfg.Server.AdminUser
					if admin == "" {
						return Warn("no admin account is configured, so global keys have " +
							"nowhere to be installed").
							WithFix("set server.admin_user, then ratline key sync")
					}
					if !system.UserExists(admin) {
						return Fail("the admin account %s does not exist", admin).
							WithFix("set server.admin_user to an account that does")
					}
					return Pass("admin account %s", admin)
				case "site":
					site, ok := env.site(ctx, k.Site)
					if !ok {
						return Fail("the site %s no longer exists", k.Site).
							WithFix("ratline key remove %s", k.Fingerprint)
					}
					if !system.UserExists(site.Owner) {
						return Fail("the site's owner %s has no account", site.Owner).
							WithFix("ratline troubleshoot %s", site.Owner)
					}
					return Pass("%s, owned by %s", site.Domain, site.Owner)
				default:
					if !system.UserExists(k.Owner) {
						return Fail("the tenant %s has no account", k.Owner).
							WithFix("ratline troubleshoot %s", k.Owner)
					}
					return Pass("tenant %s", k.Owner)
				}
			},
		},
		{
			ID:    "installed",
			Title: "the key is present in the file sshd will read",
			Needs: []string{"scope-target"},
			Run: func(context.Context) Result {
				path, _ := target()
				if path == "" {
					return Skip("there is no file for this scope")
				}
				// Bounded, not os.ReadFile: this path is the key owner's authorized_keys,
				// a file the tenant owns and can point at /dev/zero (or grow without limit).
				// An unbounded read of it in the root diagnosis process is a DoS; ReadFileLimit
				// rejects an oversized file and caps a device symlink, matching every other
				// authorized_keys reader in the tree.
				body, err := system.ReadFileLimit(path, int64(env.Cfg.SSH.MaxAuthKeysBytes))
				if err != nil {
					if os.IsNotExist(err) {
						return Fail("%s does not exist", path).
							WithFix("ratline key sync").WithTopic("ssh")
					}
					return Fail("%s could not be read: %v", path, err).
						WithFix("ratline key sync").WithTopic("ssh")
				}
				// Matched on the blob rather than the fingerprint, because the blob is
				// what sshd compares. A file listing the right fingerprint in a comment
				// and the wrong key material would authenticate nobody.
				if !strings.Contains(string(body), strings.TrimSpace(k.Blob)) {
					return Fail("the key is recorded in state but is not in %s", path).
						WithFix("ratline key sync").WithTopic("ssh")
				}
				return Pass("%s", path)
			},
		},
		{
			ID:    "file-permissions",
			Title: "sshd will accept the file's permissions",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				path, home := target()
				if home == "" {
					// The global keys file lives under /etc and is root-owned; sshd's
					// StrictModes rules are about home directories.
					fi, err := os.Stat(path)
					if err != nil {
						return Skip("the file could not be read")
					}
					if fi.Mode().Perm()&0o022 != 0 {
						return Fail("%s is mode %04o and writable by others", path, fi.Mode().Perm()).
							WithFix("chmod 0600 %s", path)
					}
					return Pass("mode %04o", fi.Mode().Perm())
				}
				// StrictModes is on by default, and when it rejects a file sshd says so
				// only in its own log — which is a bewildering way to be locked out.
				if problems := checkKeyFilePermissions(home, path); len(problems) > 0 {
					return Fail("sshd will refuse this file: %s", strings.Join(problems, "; ")).
						WithFix("chmod 0700 %s/.ssh && chmod 0600 %s", home, path).
						WithTopic("ssh")
				}
				return Pass("")
			},
		},
		{
			ID:    "revocation-list",
			Title: "the key is not on the revocation list",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				path := env.Cfg.SSH.RevokedKeys
				body, err := os.ReadFile(path)
				if err != nil {
					// This used to read "sshd tolerates a missing RevokedKeys file", which
					// is the opposite of true and is why a server got locked out.
					// sshd_config(5): "if this file is not readable, then public key
					// authentication will be refused for all users."
					//
					// So an absent list is only harmless while nothing tells sshd to read
					// it. If the drop-in names it, this key does not work — and neither
					// does any other.
					if dropInNamesRevokedList(env.Cfg.Paths.SSHDDropIn, path) {
						return Fail("sshd is told to read %s and it is not there, so it "+
							"refuses every public key on this server, not just revoked ones", path).
							WithFix("ratline key sync").WithTopic("ssh")
					}
					return Pass("no revocation list, and nothing tells sshd to read one")
				}
				if strings.Contains(string(body), strings.TrimSpace(k.Blob)) {
					// State says live, the list says revoked. sshd believes the list, so
					// this key does not work and nothing in `key list` would say why.
					return Fail("this key is on %s even though state does not record it "+
						"as revoked, and sshd believes the list", path).
						WithFix("ratline key sync")
				}
				return Pass("")
			},
		},
		{
			ID:    "sshd-reads-it",
			Title: "sshd is configured to read that file",
			Needs: []string{"installed"},
			Run: func(ctx context.Context) Result {
				if !env.Bins.Available("sshd") {
					return Skip("sshd is not installed")
				}
				eff, err := effectiveSSHDConfig(ctx, env)
				if err != nil {
					return Skip("sshd's effective configuration could not be read")
				}
				if v := eff["pubkeyauthentication"]; v == "no" {
					// Everything above can be perfect and no key will work.
					return Fail("PubkeyAuthentication is off, so no key authenticates").
						WithFix("this is a manual change to /etc/ssh; ratline does not " +
							"set it without being asked").WithTopic("ssh")
				}
				if af := eff["authorizedkeysfile"]; af != "" &&
					!strings.Contains(af, "authorized_keys") {
					return Fail("AuthorizedKeysFile is %q, which does not include "+
						"authorized_keys", af).
						WithFix("ratline installs keys into ~/.ssh/authorized_keys; " +
							"the sshd configuration points elsewhere").WithTopic("ssh")
				}
				return Pass("AuthorizedKeysFile %s", orDefault(eff["authorizedkeysfile"], "default"))
			},
		},
		{
			ID:    "capability",
			Title: "what this key can actually do",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				// Not a pass/fail: the point is to state the grant out loud, because the
				// commonest key problem is not "it cannot log in" but "it can do
				// something other than what whoever added it believed".
				var can []string
				switch {
				case k.Scope == "site" && !k.AllowShell:
					can = append(can, "file transfer only, through a forced command")
				case k.AllowShell:
					can = append(can, "a shell")
				case k.SFTPOnly:
					can = append(can, "sftp only")
				default:
					can = append(can, "a shell")
				}
				if len(k.FromCIDR) > 0 {
					can = append(can, "from "+strings.Join(k.FromCIDR, ", "))
				}
				if k.Command != "" {
					can = append(can, "forced command "+k.Command)
				}
				detail := strings.Join(can, "; ")
				if k.AllowShell && k.Scope == "site" {
					// Worth flagging every time: --allow-shell on a site key is a
					// materially different grant from what the scope implies.
					return Warn("%s — a site key with --allow-shell reaches the whole "+
						"tenant, not just the site", detail).WithTopic("ssh")
				}
				return Pass("%s", detail)
			},
		},
		{
			ID:    "used",
			Title: "the key has been used",
			Needs: []string{"installed"},
			Run: func(context.Context) Result {
				if k.LastUsedAt.IsZero() {
					if !env.Cfg.SSH.UsageScanEnabled {
						return Skip("usage scanning is turned off in configuration")
					}
					// Not a failure. It is the signal that distinguishes "this key is
					// broken" from "nobody has tried it yet", which changes what to do
					// next.
					return Warn("never seen in the auth log, so this key has not been " +
						"tried since it was added")
				}
				return Pass("last used %s from %s",
					k.LastUsedAt.UTC().Format(time.RFC3339), orDefault(k.LastUsedIP, "an unrecorded address"))
			},
		},
	}
}
