package tls

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Issue runs the whole flow: preflight, certbot, record, attach, verify.
//
// Transactional throughout. A failure at any step leaves the previous vhost in
// place and reloaded, so a site that was serving before is still serving after.
func (m *Manager) Issue(ctx context.Context, opts IssueOptions) (*IssueResult, error) {
	if err := m.Resolve(&opts); err != nil {
		return nil, err
	}
	names, err := m.Names(ctx, &opts)
	if err != nil {
		return nil, err
	}
	res := &IssueResult{Names: names, DryRun: opts.CertbotDryRun}

	if !opts.SkipPreflight {
		checks, err := m.Preflight(ctx, &opts, names)
		if err != nil {
			return nil, err
		}
		res.Preflight = checks
		if perr := PreflightError(opts.Domain, checks); perr != nil {
			if !opts.Force {
				return res, perr
			}
			m.Log.Warn("preflight failed but --force was given; continuing", "domain", opts.Domain)
		}
	}

	certName := strings.TrimPrefix(opts.Domain, "*.")

	// An existing, valid certificate that already covers everything is not worth
	// spending an attempt on.
	if !opts.Force {
		if existing, err := m.State.GetCertificate(ctx, certName); err == nil {
			covered := true
			for _, n := range names {
				if !existing.Covers(n) {
					covered = false
				}
			}
			if covered && existing.DaysRemaining(time.Now()) > m.Cfg.ACME.RenewBeforeDays && existing.Trusted() {
				m.Log.Info("a valid certificate already covers these names",
					"name", certName, "days_remaining", existing.DaysRemaining(time.Now()))
				res.Certificate = existing
				return res, nil
			}
		}
	}

	if err := m.runCertbot(ctx, opts, names, certName); err != nil {
		return res, err
	}
	if opts.CertbotDryRun {
		m.Log.Info("dry run succeeded: the challenge would have completed", "domain", opts.Domain)
		return res, nil
	}
	res.Issued = true

	// Read the result back off disk rather than trusting what was asked for: the
	// file is the truth about what the CA actually signed.
	cert, err := m.recordIssued(ctx, certName, opts, names)
	if err != nil {
		return res, err
	}
	res.Certificate = cert

	if opts.Attach {
		if err := m.Attach(ctx, certName, certName); err != nil {
			// The certificate exists and is recorded, so this is recoverable with
			// one command rather than a lost issuance.
			return res, rlerr.Wrap(err, rlerr.CodeOf(err),
				"the certificate was issued but could not be attached").
				WithHint("the certificate is safe; retry the attach with: ratline cert attach %s", certName)
		}
		res.Attached = true
		if summary, verr := m.VerifyServed(ctx, certName, cert); verr == nil {
			res.Verified = summary
		}
	}
	return res, nil
}

// runCertbot builds and runs the certbot invocation.
//
// Every value is a separate argv element. Domains and paths are never
// interpolated into a string, so a hostile domain cannot become an extra flag.
func (m *Manager) runCertbot(ctx context.Context, opts IssueOptions, names []string, certName string) error {
	args := []string{
		"certonly",
		"--non-interactive",
		"--agree-tos",
		"--email", opts.Email,
		"--cert-name", certName,
		"--key-type", opts.KeyType,
		// certbot must not touch nginx: ratline renders the vhost itself, and a
		// certbot-installed one would be overwritten on the next reconcile.
		"--no-eff-email",
		"--keep-until-expiring",
	}
	for _, n := range names {
		args = append(args, "-d", n)
	}
	switch opts.Challenge {
	case "http":
		args = append(args, "--webroot", "--webroot-path", m.Cfg.Paths.ACMEWebroot)
	case "dns":
		plugin := "dns-" + opts.DNSProvider
		args = append(args, "--"+plugin,
			"--"+plugin+"-credentials", opts.DNSCredentials,
			"--"+plugin+"-propagation-seconds", fmt.Sprint(opts.DNSPropagation))
	}
	// --server, always. Without it certbot uses its own compiled-in directory and
	// acme.directory_url and acme.staging_url — both documented settings — did
	// nothing at all. It also meant there was no way to reach a private ACME CA:
	// step-ca, an internal issuer, or Pebble in the integration suite.
	if dir := m.directoryURL(opts); dir != "" {
		args = append(args, "--server", dir)
	} else if opts.Staging {
		// No configured staging URL, so fall back to certbot's own flag.
		args = append(args, "--staging")
	}
	if opts.Force {
		args = append(args, "--force-renewal")
	}
	if opts.CertbotDryRun {
		args = append(args, "--dry-run")
	}

	timeout := m.Cfg.ACME.IssueTimeout.D()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// DNS-01 waits for propagation inside certbot, so the timeout has to cover it.
	if opts.Challenge == "dns" {
		timeout += time.Duration(opts.DNSPropagation) * time.Second * 2
	}

	m.Log.Info("requesting a certificate", "names", strings.Join(names, " "),
		"challenge", opts.Challenge, "staging", opts.Staging)

	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "certbot", Args: args, Mutates: true, Stream: true,
		Env:     m.certbotEnv(opts),
		Timeout: timeout, Label: "certbot",
	})

	// Both outcomes are recorded: the CA counts failed validations too, and the
	// rate-limit budget is worthless if it only knows about successes.
	if !opts.CertbotDryRun && !opts.Staging {
		registered, rerr := validate.RegisteredDomain(opts.Domain)
		if rerr == nil {
			outcome := state.ACMESuccess
			errClass := ""
			if err != nil {
				outcome = state.ACMEFailure
				errClass = classifyCertbotFailure(res)
			}
			if aerr := m.State.RecordACMEAttempt(ctx, &state.ACMEAttempt{
				RegisteredDomain: registered,
				Domain:           opts.Domain,
				SANSet:           state.SANSetKey(names),
				AttemptedAt:      time.Now().UTC(),
				Outcome:          outcome,
				ErrorClass:       errClass,
				Staging:          opts.Staging,
			}); aerr != nil {
				m.Log.Debug("could not record the ACME attempt", "err", aerr)
			}
		}
	}
	if err != nil {
		return m.translateCertbotError(err, res, opts.Domain)
	}
	return nil
}

// recordIssued reads the new files and stores what is really there.
func (m *Manager) recordIssued(ctx context.Context, certName string, opts IssueOptions, requested []string) (*state.Certificate, error) {
	dir := m.LineageDir(certName)
	certPath := filepath.Join(dir, "fullchain.pem")
	if !system.Exists(certPath) {
		return nil, rlerr.Preconditionf("certbot reported success but %s does not exist", certPath).
			WithHint("run 'certbot certificates' to see what it actually created")
	}
	leaf, _, err := ParsePEM(certPath)
	if err != nil {
		return nil, err
	}
	source := state.CertSourceLetsEncrypt
	if opts.Staging {
		source = state.CertSourceStaging
	}
	cert := FromX509(certName, source, leaf, certPath,
		filepath.Join(dir, "privkey.pem"), filepath.Join(dir, "chain.pem"))
	cert.Lineage = certName
	cert.Challenge = opts.Challenge
	cert.DNSProvider = opts.DNSProvider

	// If the CA signed fewer names than were asked for, say so: it is the kind of
	// difference that surfaces months later as a name mismatch.
	for _, want := range requested {
		if !cert.Covers(want) {
			m.Log.Warn("the issued certificate does not cover a requested name",
				"name", want, "sans", strings.Join(cert.SANs, " "))
		}
	}
	if err := m.State.PutCertificate(ctx, cert); err != nil {
		return nil, err
	}
	if err := m.State.RecordRenewal(ctx, certName, "success", ""); err != nil {
		m.Log.Debug("could not record the issuance", "err", err)
	}
	m.Log.Info("certificate issued", "name", certName, "issuer", cert.Issuer,
		"expires", cert.NotAfter.Format("2006-01-02"), "sans", len(cert.SANs))
	return cert, nil
}

// VerifyServed opens a real TLS connection and checks what is actually served.
//
// This is the difference between "the file is on disk" and "the site works". A
// certificate nginx has not picked up, or one served by a different vhost because
// certbotEnv is the environment certbot runs with.
//
// nil means the runner's usual minimal environment. The one addition is
// REQUESTS_CA_BUNDLE, and only for a private CA.
//
// certbot verifies the ACME server with certifi's bundled roots, not the system
// trust store — so a private CA's root installed the normal way, with
// update-ca-certificates, is not consulted and issuance fails with
// CERTIFICATE_VERIFY_FAILED. ratline deliberately does not inherit the caller's
// environment, which is right, but it also means an operator cannot export the
// variable themselves. Pointing certbot at the system store when they have asked for
// a private directory is the behaviour they meant.
//
// Never done for a public CA: certifi is the correct trust store there, and widening
// it to whatever is in the system store would be a downgrade nobody asked for.
func (m *Manager) certbotEnv(opts IssueOptions) []string {
	return m.certbotEnvForBundle(m.caBundle(opts))
}

// certbotEnvForBundle is the same, given a bundle already decided on — renewal
// works out its own, from the lineage rather than from flags.
func (m *Manager) certbotEnvForBundle(bundle string) []string {
	if bundle == "" {
		return nil
	}
	m.Log.Debug("pointing certbot at a trust store for the private ACME directory",
		"bundle", bundle)
	return system.MinimalEnv(
		"REQUESTS_CA_BUNDLE="+bundle,
		// urllib3 and some plugins read this one instead.
		"SSL_CERT_FILE="+bundle,
	)
}

// systemTrustStore is the distribution's CA bundle, or empty if there isn't one.
func systemTrustStore() string {
	for _, candidate := range []string{
		"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL family
	} {
		if system.Exists(candidate) {
			return candidate
		}
	}
	return ""
}

// caBundle is the trust store to verify a private ACME server with, or empty when
// certifi's own roots are the right answer.
func (m *Manager) caBundle(opts IssueOptions) string {
	if opts.CABundle != "" {
		return opts.CABundle
	}
	// Only for a directory the operator named explicitly. A configured
	// acme.directory_url pointing at Let's Encrypt must keep using certifi.
	if opts.DirectoryURL == "" {
		return ""
	}
	return systemTrustStore()
}

// directoryURL resolves which ACME directory this issuance should talk to.
//
// An explicit override wins, then the configured staging URL when --staging was
// asked for, then the configured production URL. Empty means "let certbot decide",
// which only happens if configuration has been emptied deliberately.
func (m *Manager) directoryURL(opts IssueOptions) string {
	if opts.DirectoryURL != "" {
		return opts.DirectoryURL
	}
	if opts.Staging {
		return m.Cfg.ACME.StagingURL
	}
	return m.Cfg.ACME.DirectoryURL
}

// of a server_name collision, looks identical on disk to a working one.
func (m *Manager) VerifyServed(ctx context.Context, domain string, expected *state.Certificate) (string, error) {
	if m.DryRun {
		return "skipped under --dry-run", nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
	target := "127.0.0.1:443"
	if err == nil && len(addrs) > 0 {
		target = net.JoinHostPort(addrs[0].String(), "443")
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "cannot connect to %s on port 443", domain)
	}
	defer conn.Close()

	// SNI set explicitly: without it a server with several vhosts answers with
	// its default certificate, and the check would pass for the wrong reason.
	client := cryptotls.Client(conn, &cryptotls.Config{
		ServerName: domain,
		// Verification is done by hand below so that a chain problem can be
		// reported distinctly from a name mismatch.
		InsecureSkipVerify: true,
		MinVersion:         cryptotls.VersionTLS12,
	})
	if err := client.HandshakeContext(ctx); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "the TLS handshake with %s failed", domain)
	}
	served := client.ConnectionState().PeerCertificates
	if len(served) == 0 {
		return "", rlerr.Preconditionf("%s completed a handshake but presented no certificate", domain)
	}
	leaf := served[0]

	if expected != nil && expected.Fingerprint != "" {
		if got := Fingerprint(leaf); got != expected.Fingerprint {
			return "", rlerr.Preconditionf(
				"%s is serving a different certificate than the one just installed", domain).
				WithHint("expected %s, got %s — another vhost may claim this server_name, "+
					"or nginx did not reload; run 'ratline doctor'", expected.Fingerprint[:23], got[:23])
		}
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition,
			"the served certificate does not cover %s", domain)
	}

	// Validate against the system roots separately, so a self-signed or staging
	// certificate reports as untrusted rather than as broken.
	trusted := true
	pool := x509.NewCertPool()
	for _, c := range served[1:] {
		pool.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: domain, Intermediates: pool}); err != nil {
		trusted = false
	}
	summary := fmt.Sprintf("%s, expires %s", leaf.Issuer.CommonName, leaf.NotAfter.Format("2006-01-02"))
	if !trusted {
		summary += " (not trusted by the system root store)"
	}
	return summary, nil
}

// classifyCertbotFailure names the failure class for the rate-limit record.
func classifyCertbotFailure(res *system.Result) string {
	if res == nil {
		return "unknown"
	}
	out := strings.ToLower(res.Stdout + res.Stderr)
	switch {
	case strings.Contains(out, "too many certificates"), strings.Contains(out, "rate limit"):
		return "rate_limited"
	case strings.Contains(out, "dns problem"), strings.Contains(out, "nxdomain"):
		return "dns"
	case strings.Contains(out, "connection refused"), strings.Contains(out, "timeout"),
		strings.Contains(out, "fetching"):
		return "connection"
	case strings.Contains(out, "unauthorized"), strings.Contains(out, "incorrect validation"):
		return "unauthorized"
	case strings.Contains(out, "txt record"):
		return "dns_propagation"
	default:
		return "other"
	}
}

// translateCertbotError turns certbot's output into something actionable.
//
// A raw certbot error is a wall of Python and URLs. Every case here is one an
// operator hits in practice, and each one names the next command.
func (m *Manager) translateCertbotError(err error, res *system.Result, domain string) error {
	out := ""
	if res != nil {
		out = res.Stdout + res.Stderr
	}
	lower := strings.ToLower(out)

	switch {
	case strings.Contains(lower, "too many certificates already issued"):
		return rlerr.RateLimitedf("the certificate authority has rate limited %s", domain).
			WithHint("this limit is per registered domain and resets a week after the oldest issuance. "+
				"Use --staging to keep testing meanwhile; it has its own, much larger limits").
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "too many failed authorizations"):
		return rlerr.RateLimitedf("too many validations for %s have failed recently", domain).
			WithHint("the limit is per hostname per hour. Fix the cause, then use --dry-run to "+
				"confirm before spending another real attempt").
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "dns problem: nxdomain"):
		return rlerr.ACMEf("the certificate authority could not resolve %s", domain).
			WithHint("the DNS record does not exist yet, or has not propagated. Check with: dig +short %s", domain).
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "connection refused"), strings.Contains(lower, "timeout during connect"):
		return rlerr.ACMEf("the certificate authority could not reach %s on port 80", domain).
			WithHint("open port 80 in the firewall. The ACME challenge is served there, and "+
				"renewal fails without it even for a site that only serves HTTPS").
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "incorrect txt record"), strings.Contains(lower, "no txt record"):
		return rlerr.ACMEf("the DNS challenge record for %s had not propagated when it was checked", domain).
			WithHint("raise --dns-propagation, or check that the API token can write to this zone").
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid response from"):
		return rlerr.ACMEf("the challenge for %s was served by something other than this server", domain).
			WithHint("a proxy or another vhost is answering. Run 'ratline cert issue %s --dry-run' "+
				"to see the preflight detail", domain).
			WithField("certbot_output", tail(out, 4))

	case strings.Contains(lower, "could not be found"), strings.Contains(lower, "no such plugin"):
		return rlerr.Preconditionf("the certbot DNS plugin is not installed").
			WithHint("apt-get install python3-certbot-dns-<provider>")

	case strings.Contains(lower, "permission denied"):
		return rlerr.Wrap(err, rlerr.CodePrecondition, "certbot could not read or write a file it needs").
			WithHint("check that %s is writable and that the DNS credentials file is 0600",
				m.Cfg.Paths.ACMEWebroot)
	}

	return rlerr.Wrap(err, rlerr.CodeACME, "the certificate for %s could not be issued", domain).
		WithField("certbot_output", tail(out, 6)).
		WithHint("certbot's own log has the detail: /var/log/letsencrypt/letsencrypt.log")
}

// tail returns the last n meaningful lines, which is where certbot puts the reason.
//
// Certbot ends every failure with the same four lines of signposting — a rule of
// dashes, a renew-failure count, "Ask for help ...", "See the logfile ..." — so a
// plain tail of four spends all four on boilerplate and shows the operator nothing
// about their own certificate.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || certbotSignpost(l) {
			continue
		}
		kept = append([]string{l}, kept...)
	}
	if len(kept) == 0 {
		// Better to repeat the signposts than to report an empty reason.
		return strings.TrimSpace(s)
	}
	return strings.Join(kept, "\n")
}

func certbotSignpost(line string) bool {
	if strings.Trim(line, "- ") == "" {
		return true
	}
	lower := strings.ToLower(line)
	for _, s := range []string{
		"ask for help",
		"see the logfile",
		"please see the logfile",
		"saving debug log",
		"renew failure(s)",
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
