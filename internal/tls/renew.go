package tls

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// RenewOptions is the resolved form of `ratline cert renew`.
type RenewOptions struct {
	Name   string // empty with All
	All    bool
	Force  bool
	DryRun bool
}

// RenewOutcome is one certificate's result.
type RenewOutcome struct {
	Name     string   `json:"name"`
	Action   string   `json:"action"` // renewed, skipped, failed
	Detail   string   `json:"detail,omitempty"`
	Reloaded []string `json:"reloaded_sites,omitempty"`
}

// Renew renews certificates that are due.
//
// A failure is not an emergency: the existing certificate is still valid for
// weeks, which is the whole reason the window is 30 days. So one certificate
// failing never stops the others, and the failure is recorded for `doctor` rather
// than shouted about.
func (m *Manager) Renew(ctx context.Context, opts RenewOptions) ([]RenewOutcome, error) {
	var targets []*state.Certificate
	if opts.All {
		all, err := m.State.ListCertificates(ctx)
		if err != nil {
			return nil, err
		}
		targets = all
	} else {
		if opts.Name == "" {
			return nil, rlerr.Usagef("name a certificate, or pass --all")
		}
		c, err := m.State.GetCertificate(ctx, opts.Name)
		if err != nil {
			return nil, err
		}
		targets = []*state.Certificate{c}
	}

	now := time.Now()
	var outcomes []RenewOutcome
	for _, cert := range targets {
		outcome := RenewOutcome{Name: cert.Name}

		switch {
		case cert.Source == state.CertSourceSelfSigned:
			outcome.Action = "skipped"
			outcome.Detail = "self-signed; issue a real certificate with 'ratline cert issue " + cert.Name + "'"
		case cert.Source == state.CertSourceImported:
			outcome.Action = "skipped"
			outcome.Detail = fmt.Sprintf("imported, so nothing renews it automatically (%d days left)",
				cert.DaysRemaining(now))
		case !cert.AutoRenew && !opts.Force:
			outcome.Action = "skipped"
			outcome.Detail = "automatic renewal is off for this certificate"
		case !opts.Force && cert.DaysRemaining(now) > m.Cfg.ACME.RenewBeforeDays:
			outcome.Action = "skipped"
			outcome.Detail = fmt.Sprintf("%d days remaining, which is more than the %d-day window",
				cert.DaysRemaining(now), m.Cfg.ACME.RenewBeforeDays)
		default:
			m.renewOne(ctx, cert, opts, &outcome)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// renewOne renews a single lineage.
func (m *Manager) renewOne(ctx context.Context, cert *state.Certificate, opts RenewOptions, outcome *RenewOutcome) {
	// Exponential backoff after repeated failures, so a broken certificate does
	// not hammer the CA twice a day and burn the failed-validation budget.
	if cert.ConsecutiveFailures > 0 && !opts.Force {
		wait := time.Duration(1<<minInt(cert.ConsecutiveFailures, 6)) * time.Hour
		if !cert.LastRenewalAt.IsZero() && time.Since(cert.LastRenewalAt) < wait {
			outcome.Action = "skipped"
			outcome.Detail = fmt.Sprintf("backing off after %d failure(s); next attempt in %s",
				cert.ConsecutiveFailures, time.Until(cert.LastRenewalAt.Add(wait)).Round(time.Minute))
			return
		}
	}

	args := []string{"renew", "--cert-name", cert.Name, "--non-interactive"}
	if opts.Force {
		args = append(args, "--force-renewal")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	// The deploy hook is what reloads only the affected sites. Passing it here
	// rather than relying on certbot's renewal config means it works even for a
	// lineage certbot created before ratline was installed.
	if !opts.DryRun {
		self, err := system.SelfPath()
		if err == nil {
			args = append(args, "--deploy-hook", self+" cert deploy-hook")
		}
	}

	timeout := m.Cfg.ACME.IssueTimeout.D()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	m.Log.Info("renewing", "name", cert.Name, "days_remaining", cert.DaysRemaining(time.Now()),
		"dry_run", opts.DryRun)

	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "certbot", Args: args, Mutates: !opts.DryRun, Stream: true,
		Timeout: timeout, Label: "certbot renew",
		// Issuance points certbot at a trust store when the directory is a private
		// CA; renewal has to do the same, or a server issuing from step-ca or an
		// internal CA gets certificates it can never renew. It fails as a hang —
		// certbot retries the TLS failure until the timeout — so the timer looks
		// slow rather than broken, and the first sign is an expired certificate.
		Env: m.certbotEnvForBundle(m.renewalCABundle(cert.Name)),
	})
	if err != nil {
		outcome.Action = "failed"
		translated := m.translateCertbotError(err, res, cert.Name)
		outcome.Detail = translated.Error()
		if !opts.DryRun {
			if rerr := m.State.RecordRenewal(ctx, cert.Name, "failure", firstLine(outcome.Detail)); rerr != nil {
				m.Log.Debug("could not record the failure", "err", rerr)
			}
			m.alert(ctx, cert, outcome.Detail)
		}
		m.Log.Error("renewal failed; the existing certificate is still valid",
			"name", cert.Name, "days_remaining", cert.DaysRemaining(time.Now()))
		return
	}

	if opts.DryRun {
		outcome.Action = "renewed"
		outcome.Detail = "dry run succeeded"
		return
	}

	// certbot says so when it decided nothing was due, which is not a failure.
	if res != nil && strings.Contains(res.Stdout+res.Stderr, "not yet due for renewal") {
		outcome.Action = "skipped"
		outcome.Detail = "certbot decided it was not due"
		return
	}

	refreshed, rerr := m.refreshFromDisk(ctx, cert)
	if rerr != nil {
		outcome.Action = "failed"
		outcome.Detail = rerr.Error()
		return
	}
	outcome.Action = "renewed"
	outcome.Detail = "expires " + refreshed.NotAfter.Format("2006-01-02")
	if err := m.State.RecordRenewal(ctx, cert.Name, "success", ""); err != nil {
		m.Log.Debug("could not record the success", "err", err)
	}

	// The deploy hook normally does this. Doing it here too covers the case where
	// certbot renewed without invoking it.
	reloaded, err := m.reloadAffected(ctx, refreshed)
	if err != nil {
		m.Log.Warn("the certificate renewed but a site could not be reloaded", "err", err)
	}
	outcome.Reloaded = reloaded
}

// refreshFromDisk re-reads a renewed lineage.
func (m *Manager) refreshFromDisk(ctx context.Context, cert *state.Certificate) (*state.Certificate, error) {
	path := cert.CertPath
	if path == "" {
		path = m.LineageDir(cert.Name) + "/fullchain.pem"
	}
	leaf, _, err := ParsePEM(path)
	if err != nil {
		return nil, err
	}
	updated := FromX509(cert.Name, cert.Source, leaf, path, cert.KeyPath, cert.ChainPath)
	updated.Lineage = cert.Lineage
	updated.Challenge = cert.Challenge
	updated.DNSProvider = cert.DNSProvider
	updated.AutoRenew = cert.AutoRenew
	updated.CreatedAt = cert.CreatedAt
	if err := m.State.PutCertificate(ctx, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// reloadAffected reloads only the sites using a certificate.
//
// Never a blanket restart: on a server with forty sites, reloading all of them
// because one certificate changed is forty chances to notice an unrelated
// configuration problem at the worst possible moment.
func (m *Manager) reloadAffected(ctx context.Context, cert *state.Certificate) ([]string, error) {
	fresh, err := m.State.GetCertificate(ctx, cert.Name)
	if err != nil {
		return nil, err
	}
	if len(fresh.Attached) == 0 {
		m.Log.Debug("the renewed certificate is not attached to any site", "name", cert.Name)
		return nil, nil
	}
	var reloaded []string
	for _, domain := range fresh.Attached {
		site, err := m.State.FindSiteByName(ctx, domain)
		if err != nil {
			m.Log.Warn("a certificate is attached to a site that no longer exists",
				"cert", cert.Name, "domain", domain, "fix", "ratline cert detach "+domain)
			continue
		}
		rb := system.NewRollback(m.Log)
		if err := m.Nginx.Apply(ctx, site, fresh, rb); err != nil {
			return reloaded, err
		}
		rb.Commit()
		reloaded = append(reloaded, domain)
	}
	return reloaded, nil
}

// DeployHook is what certbot invokes after a successful renewal.
//
// certbot sets RENEWED_LINEAGE and RENEWED_DOMAINS. The hook maps them back to
// sites through state, tests the configuration, and reloads only what changed.
func (m *Manager) DeployHook(ctx context.Context) ([]string, error) {
	lineage := os.Getenv("RENEWED_LINEAGE")
	domains := strings.Fields(os.Getenv("RENEWED_DOMAINS"))

	name := ""
	switch {
	case lineage != "":
		// RENEWED_LINEAGE is /etc/letsencrypt/live/<name>.
		parts := strings.Split(strings.TrimRight(lineage, "/"), "/")
		name = parts[len(parts)-1]
	case len(domains) > 0:
		name = domains[0]
	default:
		return nil, rlerr.Preconditionf("neither RENEWED_LINEAGE nor RENEWED_DOMAINS is set").
			WithHint("this command is meant to be run by certbot as a deploy hook, not by hand")
	}
	m.Log.Info("deploy hook", "lineage", name, "domains", strings.Join(domains, " "))

	cert, err := m.State.GetCertificate(ctx, name)
	if err != nil {
		// A lineage certbot renewed that ratline does not know about: adopt it
		// rather than ignoring the renewal.
		if _, serr := m.Scan(ctx); serr != nil {
			return nil, err
		}
		if cert, err = m.State.GetCertificate(ctx, name); err != nil {
			return nil, err
		}
	}
	refreshed, err := m.refreshFromDisk(ctx, cert)
	if err != nil {
		return nil, err
	}
	if err := m.State.RecordRenewal(ctx, name, "success", ""); err != nil {
		m.Log.Debug("could not record the renewal", "err", err)
	}
	return m.reloadAffected(ctx, refreshed)
}

// TestRenewal dry-runs every managed certificate, so breakage is found weeks
// before an expiry rather than on the morning it happens.
func (m *Manager) TestRenewal(ctx context.Context) ([]RenewOutcome, error) {
	certs, err := m.State.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	var outcomes []RenewOutcome
	for _, cert := range certs {
		outcome := RenewOutcome{Name: cert.Name}
		switch cert.Source {
		case state.CertSourceSelfSigned:
			outcome.Action = "skipped"
			outcome.Detail = "self-signed"
		case state.CertSourceImported:
			outcome.Action = "skipped"
			outcome.Detail = fmt.Sprintf("imported; nothing renews it (%d days left)", cert.DaysRemaining(time.Now()))
		default:
			// --force so the dry run actually exercises the challenge rather than
			// deciding nothing is due.
			m.renewOne(ctx, cert, RenewOptions{DryRun: true, Force: true}, &outcome)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// alert notifies about a renewal failure, if configured.
func (m *Manager) alert(ctx context.Context, cert *state.Certificate, detail string) {
	url := m.Cfg.ACME.Alerts.WebhookURL
	if url == "" {
		return
	}
	days := cert.DaysRemaining(time.Now())
	body := fmt.Sprintf(
		`{"text":"ratline: renewal failed for %s (%d days remaining): %s"}`,
		cert.Name, days, strings.ReplaceAll(firstLine(detail), `"`, `'`))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.Log.Debug("the alert webhook failed", "err", err)
		return
	}
	defer resp.Body.Close()
	m.Log.Debug("alert sent", "status", resp.StatusCode)
}

// AutoRenewStatus is the state of automatic renewal on this host.
type AutoRenewStatus struct {
	TimerInstalled bool     `json:"timer_installed"`
	TimerActive    bool     `json:"timer_active"`
	NextRun        string   `json:"next_run,omitempty"`
	CertbotTimer   string   `json:"certbot_timer"`
	Enabled        []string `json:"enabled"`
	Disabled       []string `json:"disabled"`
}

// AutoRenewState reports whether renewal is actually wired up.
func (m *Manager) AutoRenewState(ctx context.Context) (*AutoRenewStatus, error) {
	s := &AutoRenewStatus{CertbotTimer: "not present"}

	res, err := m.Runner.Run(ctx, system.Cmd{
		Name:   "systemctl",
		Args:   []string{"show", "ratline-cert-renew.timer", "--property=LoadState,ActiveState,NextElapseUSecRealtime"},
		OKExit: []int{1, 3, 4},
	})
	if err == nil && res != nil {
		for _, line := range res.Lines() {
			key, value, _ := strings.Cut(line, "=")
			switch key {
			case "LoadState":
				s.TimerInstalled = value == "loaded"
			case "ActiveState":
				s.TimerActive = value == "active"
			case "NextElapseUSecRealtime":
				if value != "" && value != "n/a" {
					s.NextRun = value
				}
			}
		}
	}

	// certbot's own timer racing ratline's is a real problem: each reloads nginx
	// from under the other, and only ratline's runs the deploy hook.
	if res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"is-enabled", "certbot.timer"}, OKExit: []int{1},
	}); err == nil && res != nil {
		if state := strings.TrimSpace(res.Out()); state == "enabled" {
			s.CertbotTimer = "enabled — it will race ratline's timer"
		} else if state != "" {
			s.CertbotTimer = state
		}
	}

	certs, err := m.State.ListCertificates(ctx)
	if err != nil {
		return s, err
	}
	for _, c := range certs {
		if c.AutoRenew {
			s.Enabled = append(s.Enabled, c.Name)
		} else {
			s.Disabled = append(s.Disabled, c.Name)
		}
	}
	return s, nil
}

// AccountInfo describes the ACME account.
type AccountInfo struct {
	Email      string `json:"email"`
	Directory  string `json:"directory_url"`
	TOSAgreed  bool   `json:"tos_agreed"`
	Registered bool   `json:"registered"`
}

// Account reports the ACME account state.
func (m *Manager) Account(ctx context.Context) *AccountInfo {
	info := &AccountInfo{
		Email:     m.Cfg.ACME.Email,
		Directory: m.Cfg.ACME.DirectoryURL,
		TOSAgreed: m.Cfg.ACME.TOSAgreed,
	}
	// certbot keeps its accounts under /etc/letsencrypt/accounts; its presence is
	// the only reliable evidence of registration.
	info.Registered = system.IsDir(m.Cfg.Paths.LetsEncryptDir + "/accounts")
	return info
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renewalCABundle is the trust store to verify the ACME server with when renewing
// this lineage, or empty when certifi's own roots are the right answer.
//
// Renewal cannot use the issuance rule — "the operator named a directory on the
// command line" — because there is no command line: certbot reads the server out of
// the lineage's own renewal config, which is exactly right and also invisible to the
// flags. So this reads the same file certbot does.
//
// Anything that is not Let's Encrypt is treated as private. That is deliberately the
// wide side of the judgement: pointing certbot at the system trust store for a public
// CA it could already verify changes nothing, while missing a private one means the
// certificate silently stops renewing.
func (m *Manager) renewalCABundle(certName string) string {
	server := m.renewalServer(certName)
	if server == "" {
		// No conf, or no server line: certbot's default is Let's Encrypt.
		return ""
	}
	for _, public := range []string{
		"acme-v02.api.letsencrypt.org",
		"acme-staging-v02.api.letsencrypt.org",
	} {
		if strings.Contains(server, public) {
			return ""
		}
	}
	return systemTrustStore()
}

// renewalServer reads the `server = ...` line from a lineage's renewal config.
func (m *Manager) renewalServer(certName string) string {
	path := filepath.Join(m.Cfg.Paths.LetsEncryptDir, "renewal", certName+".conf")
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "server" {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}
