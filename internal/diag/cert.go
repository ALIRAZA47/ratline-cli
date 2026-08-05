package diag

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
	rtls "github.com/ALIRAZA47/ratline-cli/internal/tls"
)

func certSummary(c *state.Certificate) string {
	parts := []string{c.Source}
	if c.Issuer != "" {
		parts = append(parts, "issued by "+c.Issuer)
	}
	return strings.Join(parts, ", ")
}

// CertChecks diagnose a certificate from the files outwards to the handshake.
//
// The handshake is the point. A certificate can be present, valid, current and
// attached in state, and nginx can still be serving something else entirely —
// because it loaded the old one and was never reloaded. Only a real connection
// distinguishes those, and it is the last check for that reason: everything before
// it explains why the handshake would fail.
func CertChecks(env *Env, c *state.Certificate) []Check {
	return []Check{
		{
			ID:    "files",
			Title: "the certificate and key are on disk",
			Run: func(context.Context) Result {
				for _, f := range []struct{ label, path string }{
					{"certificate", c.CertPath},
					{"private key", c.KeyPath},
				} {
					if f.path == "" {
						return Fail("no %s path is recorded", f.label).
							WithFix("ratline cert list --json shows what state holds")
					}
					if !exists(f.path) {
						return Fail("the %s is missing from %s", f.label, f.path).
							WithFix("ratline cert renew %s --force, or re-import it", c.Name)
					}
				}
				return Pass("%s", c.CertPath)
			},
		},
		{
			ID:    "key-permissions",
			Title: "the private key is not readable by anyone else",
			Needs: []string{"files"},
			Run: func(context.Context) Result {
				fi, err := os.Stat(c.KeyPath)
				if err != nil {
					return Skip("the key could not be read")
				}
				if fi.Mode().Perm()&0o077 != 0 {
					return Fail("%s is mode %04o, and a private key must be 0600",
						c.KeyPath, fi.Mode().Perm()).
						WithFix("chmod 0600 %s", c.KeyPath)
				}
				return Pass("mode %04o", fi.Mode().Perm())
			},
		},
		{
			ID:    "parses",
			Title: "the certificate parses and matches its record",
			Needs: []string{"files"},
			Run: func(context.Context) Result {
				leaf, _, err := rtls.ParsePEM(c.CertPath)
				if err != nil {
					return Fail("%s does not parse: %s", c.CertPath, firstLine(err.Error())).
						WithFix("ratline cert renew %s --force", c.Name)
				}
				// State is an index, so the file wins — but a mismatch means renewal
				// happened and ratline did not see it, which is worth knowing.
				if fp := rtls.Fingerprint(leaf); c.Fingerprint != "" && fp != c.Fingerprint {
					return Warn("the file on disk is not the certificate state records").
						WithFix("ratline reconcile, then ratline cert list")
				}
				return Pass("%s, %s", rtls.KeyTypeOf(leaf), rtls.SANSummary(rtls.SANsOf(leaf), 3))
			},
		},
		{
			ID:    "validity",
			Title: "the certificate is inside its validity window",
			Needs: []string{"parses"},
			Run: func(context.Context) Result {
				now := time.Now()
				switch {
				case !c.NotBefore.IsZero() && now.Before(c.NotBefore):
					// Almost always a clock problem rather than a certificate problem,
					// and worth saying so because the instinct is to reissue.
					return Fail("not valid until %s — check this server's clock",
						c.NotBefore.UTC().Format(time.RFC3339)).
						WithFix("timedatectl status").WithTopic("tls")
				case !c.NotAfter.IsZero() && now.After(c.NotAfter):
					return Fail("expired on %s", c.NotAfter.UTC().Format(time.RFC3339)).
						WithFix("ratline cert renew %s --force", c.Name).WithTopic("tls")
				}
				days := c.DaysRemaining(now)
				if days < env.Cfg.ACME.RenewBeforeDays {
					return Warn("%s left, which is inside the renewal window", plural(days, "day")).
						WithFix("ratline cert renew %s", c.Name).WithTopic("tls")
				}
				return Pass("%s left", plural(days, "day"))
			},
		},
		{
			ID:    "renewable",
			Title: "renewal is set up and has been working",
			Needs: []string{"validity"},
			Run: func(context.Context) Result {
				if !c.AutoRenew {
					return Warn("auto-renewal is off for this certificate").
						WithFix("ratline cert auto-renew enable %s", c.Name)
				}
				if c.ConsecutiveFailures > 0 {
					// The failure mode this exists for: renewal has been failing quietly
					// for a fortnight and nobody looks until the certificate expires.
					detail := fmt.Sprintf("the last %s failed",
						plural(c.ConsecutiveFailures, "renewal attempt"))
					if c.LastRenewalError != "" {
						detail += ": " + firstLine(c.LastRenewalError)
					}
					return Fail("%s", detail).
						WithFix("ratline cert renew %s --dry-run to see it fail in full", c.Name).
						WithTopic("tls")
				}
				if c.Source == state.CertSourceSelfSigned {
					return Warn("self-signed certificates are not renewed automatically").
						WithFix("ratline cert issue %s once DNS points here", c.Name)
				}
				return Pass("auto-renewal on")
			},
		},
		{
			ID:    "renewal-trust",
			Title: "renewal can verify the certificate authority",
			Needs: []string{"renewable"},
			Run: func(context.Context) Result {
				// certbot verifies the ACME directory against certifi's bundled roots,
				// not the system trust store, so a private CA needs acme.ca_bundle. This
				// costs nothing to check and is otherwise invisible until the
				// certificate expires: issuance passes a bundle on the command line,
				// renewal has no command line, and the failure is a TLS error inside
				// certbot that reads as a network problem.
				server := renewalServerFor(env, c.Name)
				if server == "" || isPublicACME(server) {
					return Pass("Let's Encrypt, verified with certbot's own roots")
				}
				if bundle := acmeCABundle(env); bundle != "" {
					if !exists(bundle) {
						return Fail("acme.ca_bundle points at %s, which does not exist", bundle).
							WithFix("correct acme.ca_bundle in /etc/ratline/config.yaml").
							WithTopic("tls")
					}
					return Pass("%s, verified with %s", server, bundle)
				}
				return Fail("this certificate renews from %s, and no acme.ca_bundle is set", server).
					WithFix("set acme.ca_bundle in /etc/ratline/config.yaml to the CA's root; "+
						"certbot cannot verify a private ACME server without it, and "+
						"'ratline cert renew %s --dry-run' will show it failing", c.Name).
					WithTopic("tls")
			},
		},
		{
			ID:    "attached",
			Title: "the certificate is attached to a vhost",
			Needs: []string{"parses"},
			Run: func(context.Context) Result {
				if len(c.Attached) == 0 {
					// Not broken, but it is doing nothing — and it still consumes rate
					// limit on renewal.
					return Warn("attached to no site, so nothing serves it").
						WithFix("ratline cert attach <domain> %s, or delete it", c.Name)
				}
				return Pass("%s", strings.Join(c.Attached, ", "))
			},
		},
		{
			ID:    "served",
			Title: "this is the certificate actually being served",
			Needs: []string{"attached"},
			Run: func(ctx context.Context) Result {
				domain := c.Attached[0]
				served, err := servedCertificate(ctx, env, domain)
				if err != nil {
					return Warn("could not complete a TLS handshake for %s: %s",
						domain, firstLine(err.Error())).
						WithFix("ratline troubleshoot %s", domain)
				}
				if c.Fingerprint != "" && served != c.Fingerprint {
					// The distinction nothing else can make: on disk and loaded are not
					// the same thing, and nginx keeps the old one until it is reloaded.
					return Fail("%s is serving a different certificate from the one on disk — "+
						"nginx has not reloaded since it changed", domain).
						WithFix("systemctl reload nginx").WithTopic("tls")
				}
				return Pass("%s serves this certificate", domain)
			},
		},
	}
}

// servedCertificate connects over TLS and returns the fingerprint of what comes
// back.
//
// To the loopback with SNI set, so it works before DNS points here and does not
// depend on the public network path. Verification is off deliberately: the question
// is *which* certificate is being served, and a self-signed or expired one still
// answers it — refusing the handshake would hide exactly the case being diagnosed.
func servedCertificate(ctx context.Context, env *Env, domain string) (string, error) {
	ctx, cancel := env.probeContext(ctx)
	defer cancel()
	dialer := &net.Dialer{Timeout: env.probeTimeout()}
	conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := tls.Client(conn, &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: true, // asking what is served, not whether it is trusted
		MinVersion:         tls.VersionTLS12,
	})
	defer client.Close()
	if err := client.HandshakeContext(ctx); err != nil {
		return "", err
	}
	certs := client.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("the server presented no certificate")
	}
	return rtls.Fingerprint(certs[0]), nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// acmeCABundle is the configured trust store for a private ACME CA, if any.
func acmeCABundle(env *Env) string {
	if env == nil || env.Cfg == nil {
		return ""
	}
	return env.Cfg.ACME.CABundle
}

// renewalServerFor is the ACME directory a lineage will really renew from, read from
// the same file certbot reads. Delegated so the sweep, the walk and the renewal itself
// cannot come to different conclusions about the same certificate.
func renewalServerFor(env *Env, certName string) string {
	if env == nil {
		return ""
	}
	return rtls.RenewalServer(env.Cfg, certName)
}

func isPublicACME(server string) bool { return rtls.IsPublicACME(server) }
