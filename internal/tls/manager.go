// Package tls manages certificates as a first-class resource with their own
// lifecycle, independent of the sites that use them.
//
// That independence is the point: a site can be created and serving HTTP before
// DNS has been pointed at the server, and have a real certificate issued and
// attached later. A tool that demanded working DNS before it would create a site
// would make the normal order of operations impossible.
//
// certbot is an implementation detail behind this package. Its output is parsed
// and translated; a raw certbot error is never surfaced to an operator.
package tls

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Manager performs certificate operations.
type Manager struct {
	Cfg     *config.Config
	Log     *log.Logger
	Runner  system.Runner
	State   *state.Store
	Nginx   *nginx.Manager
	Invoker string
	DryRun  bool
}

// Status is the value in the STATUS column of `cert list`.
type Status string

const (
	StatusValid      Status = "valid"
	StatusExpiring   Status = "expiring" // under 21 days
	StatusCritical   Status = "critical" // under 7 days
	StatusExpired    Status = "expired"
	StatusDegraded   Status = "degraded"    // the last renewal failed
	StatusStaging    Status = "staging"     // real, but untrusted by browsers
	StatusSelfSigned Status = "self-signed" // a placeholder
	StatusOrphaned   Status = "orphaned"    // no site attached
	StatusMismatch   Status = "unattached-mismatch"
	StatusUnmanaged  Status = "unmanaged" // on disk, not in state
)

// StatusOf classifies a certificate. Order matters: expiry beats provenance,
// because an expired staging certificate is an expiry problem first.
func StatusOf(c *state.Certificate, now time.Time) Status {
	days := c.DaysRemaining(now)
	switch {
	case c.NotAfter.IsZero():
		return StatusUnmanaged
	case days < 0:
		return StatusExpired
	case c.ConsecutiveFailures > 0:
		return StatusDegraded
	case days < 7:
		return StatusCritical
	case c.Source == state.CertSourceSelfSigned:
		return StatusSelfSigned
	case c.Source == state.CertSourceStaging:
		return StatusStaging
	case days < 21:
		return StatusExpiring
	case len(c.Attached) == 0:
		return StatusOrphaned
	default:
		return StatusValid
	}
}

// IssueOptions is the resolved form of `ratline cert issue`.
type IssueOptions struct {
	Domain      string
	Aliases     []string
	ExtraSANs   []string
	Challenge   string // http or dns
	DNSProvider string
	// DNSHook and DNSCleanupHook are the scripts certbot's manual plugin calls to
	// publish and withdraw the TXT record, for a DNS provider certbot has no plugin
	// for. Only meaningful with --dns-provider manual.
	DNSHook        string
	DNSCleanupHook string
	DNSCredentials string
	DNSPropagation int
	Email          string
	Staging        bool
	// DirectoryURL overrides the ACME directory for this one issuance. Empty means
	// the configured one — acme.staging_url when Staging, acme.directory_url
	// otherwise.
	DirectoryURL string
	// CABundle is the trust store certbot verifies the ACME server with. Only
	// meaningful for a private CA; see caBundle.
	CABundle      string
	KeyType       string
	Force         bool
	Attach        bool
	CertbotDryRun bool
	SkipPreflight bool
}

// IssueResult reports what happened.
type IssueResult struct {
	Certificate *state.Certificate `json:"certificate"`
	Names       []string           `json:"names"`
	Issued      bool               `json:"issued"`
	Attached    bool               `json:"attached"`
	Verified    string             `json:"verified,omitempty"`
	DryRun      bool               `json:"dry_run,omitempty"`
	Preflight   []PreflightResult  `json:"preflight,omitempty"`
}

// Names resolves the full SAN list for an issuance: the domain, its registered
// aliases unless overridden, and any extra SANs.
func (m *Manager) Names(ctx context.Context, opts *IssueOptions) ([]string, error) {
	domain, err := validate.DomainOrWildcard(opts.Domain)
	if err != nil {
		return nil, err
	}
	names := []string{domain}
	seen := map[string]bool{domain: true}

	candidates := opts.Aliases
	if len(candidates) == 0 {
		// Default to the site's own aliases: a certificate that does not cover
		// the www host a site already serves is a name mismatch waiting to happen.
		if site, err := m.State.FindSiteByName(ctx, strings.TrimPrefix(domain, "*.")); err == nil {
			candidates = site.Aliases
		}
	}
	for _, raw := range append(candidates, opts.ExtraSANs...) {
		name, err := validate.DomainOrWildcard(raw)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) > 100 {
		// The CA limit is 100 names per certificate.
		return nil, rlerr.Usagef("a certificate cannot carry %d names; the limit is 100", len(names))
	}
	return names, nil
}

// Resolve fills in the defaults and checks the options against each other.
func (m *Manager) Resolve(opts *IssueOptions) error {
	if opts.Challenge == "" {
		opts.Challenge = "http"
	}
	// A wildcard cannot be validated over HTTP-01: the challenge is served from a
	// path on one host, and there is no host to ask for "any subdomain".
	if validate.IsWildcard(opts.Domain) && opts.Challenge != "dns" {
		if opts.Challenge == "http" {
			m.Log.Info("a wildcard requires DNS-01, so the challenge was switched",
				"why", "HTTP-01 proves control of one hostname; a wildcard covers names that do not exist yet")
			opts.Challenge = "dns"
		}
	}
	switch opts.Challenge {
	case "http":
		if opts.DNSProvider != "" {
			return rlerr.Usagef("--dns-provider only applies to --challenge dns")
		}
	case "dns":
		if opts.DNSProvider == "" {
			return rlerr.Usagef("--challenge dns requires --dns-provider").
				WithHint("for example --dns-provider cloudflare --dns-credentials /etc/ratline/dns/cloudflare.ini, " +
					"or --dns-provider manual --dns-hook /etc/ratline/dns/publish.sh for a provider " +
					"certbot has no plugin for")
		}
		// "manual" is certbot's own escape hatch, and the only way to use DNS-01 with a
		// provider it has no plugin for — which is most of them. The credentials file is
		// replaced by a script that publishes the TXT record however it needs to.
		if opts.DNSProvider == DNSProviderManual {
			if opts.DNSHook == "" {
				return rlerr.Usagef("--dns-provider manual requires --dns-hook").
					WithHint("the script receives CERTBOT_DOMAIN and CERTBOT_VALIDATION and must " +
						"publish a TXT record at _acme-challenge.$CERTBOT_DOMAIN")
			}
			if opts.DNSCredentials != "" {
				return rlerr.Usagef("--dns-credentials does not apply to --dns-provider manual").
					WithHint("the hook script is responsible for its own credentials")
			}
			// certbot executes these as root with the validation token in the
			// environment, so anyone who can write one can run code as root.
			if err := system.CheckRootOwnedExecutable(opts.DNSHook); err != nil {
				return rlerr.Wrap(err, rlerr.CodeOf(err), "the DNS hook is not safe to run as root")
			}
			if opts.DNSCleanupHook != "" {
				if err := system.CheckRootOwnedExecutable(opts.DNSCleanupHook); err != nil {
					return rlerr.Wrap(err, rlerr.CodeOf(err),
						"the DNS cleanup hook is not safe to run as root")
				}
			}
			break
		}
		if opts.DNSCredentials == "" {
			return rlerr.Usagef("--challenge dns requires --dns-credentials")
		}
		if opts.DNSHook != "" {
			return rlerr.Usagef("--dns-hook only applies to --dns-provider manual").
				WithHint("%s has a certbot plugin, which uses --dns-credentials instead", opts.DNSProvider)
		}
	default:
		return rlerr.Usagef("--challenge must be http or dns, got %q", opts.Challenge)
	}

	// Validated here so a typo is a usage error rather than a confusing certbot
	// failure fifteen seconds later.
	if opts.DirectoryURL != "" && !strings.HasPrefix(opts.DirectoryURL, "https://") {
		return rlerr.Usagef("--acme-directory must be an https URL, got %q", opts.DirectoryURL).
			WithHint("an ACME directory looks like https://acme.example.internal/directory")
	}

	if opts.KeyType == "" {
		opts.KeyType = m.Cfg.ACME.KeyType
	}
	switch opts.KeyType {
	case "ecdsa", "rsa":
	default:
		return rlerr.Usagef("--key-type must be ecdsa or rsa, got %q", opts.KeyType)
	}

	if opts.Email == "" {
		opts.Email = m.Cfg.ACME.Email
	}
	if opts.Email == "" {
		return rlerr.Usagef("an ACME contact address is required").
			WithHint("pass --email, or set acme.email once with 'ratline init'")
	}
	if err := validate.Email(opts.Email); err != nil {
		return err
	}
	if opts.DNSPropagation <= 0 {
		opts.DNSPropagation = m.Cfg.ACME.DNSPropagationSeconds
	}
	if !m.Cfg.ACME.TOSAgreed && !opts.Staging && !opts.CertbotDryRun {
		return rlerr.Preconditionf("the certificate authority's subscriber agreement has not been accepted").
			WithHint("run 'ratline init --agree-tos', or read it first: https://letsencrypt.org/repository/")
	}
	return nil
}

// LineageDir is where certbot keeps a lineage's current files.
func (m *Manager) LineageDir(name string) string {
	return fmt.Sprintf("%s/live/%s", m.Cfg.Paths.LetsEncryptDir, name)
}

// ImportedDir is where an imported certificate lives.
func (m *Manager) ImportedDir(domain string) string {
	return fmt.Sprintf("%s/%s", m.Cfg.Paths.ImportedCerts, domain)
}

// Attach points a site's vhost at a certificate and reloads nginx.
//
// This is steps 4 to 7 of the issue flow on their own, so an existing or imported
// certificate can serve another vhost — a shared SAN certificate, or an apex and
// www split across two server blocks.
func (m *Manager) Attach(ctx context.Context, domain, certName string) (err error) {
	site, err := m.State.FindSiteByName(ctx, domain)
	if err != nil {
		return err
	}
	cert, err := m.State.GetCertificate(ctx, certName)
	if err != nil {
		return err
	}

	// A certificate that does not cover the names the site serves would make
	// every browser show a name mismatch, which is worse than plain HTTP.
	var uncovered []string
	for _, name := range site.ServerNames() {
		if !cert.Covers(name) {
			uncovered = append(uncovered, name)
		}
	}
	if len(uncovered) > 0 {
		return rlerr.Preconditionf("%s does not cover %s", certName, strings.Join(uncovered, ", ")).
			WithHint("issue a certificate for those names: ratline cert issue %s --force", site.Domain)
	}
	if site.HSTS && !cert.Trusted() {
		return rlerr.Preconditionf("%s has HSTS enabled, and %s is a %s certificate", site.Domain, certName, cert.Source).
			WithHint("a browser that has seen HSTS refuses plain HTTP afterwards, so pinning it to an " +
				"untrusted certificate would lock the site out of its own domain")
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return err
	}
	if err := m.State.AttachCertificate(ctx, certName, site.Domain); err != nil {
		return err
	}
	rb.Push("attached "+certName+" to "+site.Domain, func(ctx context.Context) error {
		return m.State.DetachCertificate(ctx, site.Domain)
	})

	// Verify for real. A certificate on disk that is not being served is a
	// failure, not a success.
	if summary, verr := m.VerifyServed(ctx, site.Domain, cert); verr != nil {
		// The advice depends on how far it got. "DNS may not point here yet" is the
		// right thing to say when nothing answered; it is actively misleading when a
		// handshake completed and the wrong certificate came back, which is a
		// server_name collision or an nginx that did not reload.
		note := "this is normal if DNS does not yet resolve to this server"
		if strings.Contains(verr.Error(), "serving a different certificate") {
			note = "a handshake succeeded, so DNS is fine: another vhost claims this " +
				"server_name, or nginx is still serving the old certificate"
		}
		m.Log.Warn("the certificate was attached but could not be verified over the network",
			"domain", site.Domain, "err", verr, "note", note)
	} else {
		m.Log.Info("verified", "domain", site.Domain, "served", summary)
	}
	return nil
}

// Detach reverts a site to HTTP-only.
func (m *Manager) Detach(ctx context.Context, domain string) (err error) {
	site, err := m.State.FindSiteByName(ctx, domain)
	if err != nil {
		return err
	}
	if err := m.State.DetachCertificate(ctx, site.Domain); err != nil {
		return err
	}
	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)
	// Re-rendered with no certificate, which drops the port-443 block and stops
	// the HTTP redirect.
	if err := m.Nginx.Apply(ctx, site, nil, rb); err != nil {
		return err
	}
	m.Log.Info("certificate detached; the site now serves plain HTTP", "domain", site.Domain)
	return nil
}

// Delete removes a certificate, refusing while a site still uses it.
func (m *Manager) Delete(ctx context.Context, name string, keepFiles bool) error {
	cert, err := m.State.GetCertificate(ctx, name)
	if err != nil {
		return err
	}
	if len(cert.Attached) > 0 {
		return rlerr.Preconditionf("%s is still used by %s", name, strings.Join(cert.Attached, ", ")).
			WithHint("detach it first: ratline cert detach %s", cert.Attached[0])
	}
	if !keepFiles && cert.Source != state.CertSourceImported && m.Bins("certbot") {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "certbot", Args: []string{"delete", "--cert-name", name, "--non-interactive"},
			Mutates: true, OKExit: []int{1}, Label: "certbot delete",
		}); err != nil {
			m.Log.Warn("certbot could not remove the lineage; the state row was removed anyway", "err", err)
		}
	}
	return m.State.DeleteCertificate(ctx, name)
}

// Revoke asks the CA to revoke a certificate, then removes it.
func (m *Manager) Revoke(ctx context.Context, name, reason string) error {
	cert, err := m.State.GetCertificate(ctx, name)
	if err != nil {
		return err
	}
	if cert.Source == state.CertSourceSelfSigned {
		return rlerr.Preconditionf("%s is self-signed, so there is no authority to revoke it", name).
			WithHint("delete it instead: ratline cert delete %s", name)
	}
	switch reason {
	case "", "unspecified", "keycompromise", "superseded", "cessationofoperation":
	default:
		return rlerr.Usagef("unknown revocation reason %q", reason).
			WithHint("use keycompromise, superseded or cessationofoperation")
	}
	if reason == "" {
		reason = "unspecified"
	}
	args := []string{"revoke", "--cert-name", name, "--reason", reason, "--non-interactive", "--delete-after-revoke"}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "certbot", Args: args, Mutates: true, Timeout: 5 * time.Minute, Label: "certbot revoke",
	}); err != nil {
		return m.translateCertbotError(err, nil, cert.Name)
	}
	if err := m.State.DetachCertificate(ctx, name); err != nil {
		return err
	}
	return m.State.DeleteCertificate(ctx, name)
}

// SetAutoRenew turns automatic renewal on or off.
func (m *Manager) SetAutoRenew(ctx context.Context, name string, on bool) error {
	return m.State.SetAutoRenew(ctx, name, on)
}

// Bins reports whether a binary is available, without an error.
func (m *Manager) Bins(name string) bool {
	type available interface{ Available(string) bool }
	if b, ok := any(m.Runner).(available); ok {
		return b.Available(name)
	}
	res, err := m.Runner.Run(context.Background(), system.Cmd{Name: name, Args: []string{"--version"}, OKExit: []int{1}})
	return err == nil && res != nil
}

// DNSProviderManual selects certbot's manual plugin, driven by a hook script.
//
// certbot ships plugins for around a dozen DNS providers. For every other provider —
// and for a company's internal DNS — the manual plugin plus a script is the only way to
// use DNS-01 at all, which is what wildcards require.
const DNSProviderManual = "manual"
