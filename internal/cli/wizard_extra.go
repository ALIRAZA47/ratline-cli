package cli

import (
	"context"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// hardeningNames lists the directives --relax accepts.
func hardeningNames() []string {
	out := make([]string, 0, len(unit.HardeningDirectives))
	for _, h := range unit.HardeningDirectives {
		out = append(out, h.Name)
	}
	return out
}

// keyWizardResult is what the key wizard collected.
type keyWizardResult struct {
	Label   string
	Grant   sshkey.Grant
	KeyRefs []string
	Argv    []string
}

// wizardKeyAdd walks an operator through granting SSH access.
//
// The scope choice is the one people get wrong, so it leads with what each scope
// actually grants rather than with its name, and previews the exact access before
// anything is written.
func wizardKeyAdd(g *Globals, ctx context.Context, res keyWizardResult) (keyWizardResult, error) {
	p := newPrompter(g)
	st, err := g.Store(ctx)
	if err != nil {
		return res, err
	}
	p.heading("Grant SSH access")

	if res.Grant.Scope == "" {
		scope, err := p.pick("Who is this key for?", []choice{
			{Value: state.ScopeGlobal, Label: "You or your ops team",
				Note: "a shell as the administrator, and permission to run ratline"},
			{Value: state.ScopeUser, Label: "A client who owns sites",
				Note: "a shell, and every site that user owns"},
			{Value: state.ScopeSite, Label: "A contractor or CI job on one site",
				Note: "sftp, rsync and git inside one directory; no shell"},
		}, state.ScopeUser)
		if err != nil {
			return res, err
		}
		res.Grant.Scope = scope
	}

	switch res.Grant.Scope {
	case state.ScopeUser:
		if res.Grant.User == "" {
			users, err := st.ListUsers(ctx)
			if err != nil {
				return res, err
			}
			if len(users) == 0 {
				return res, rlerr.Preconditionf("there are no tenants yet").
					WithHint("create one first: ratline user add <username>")
			}
			options := make([]choice, 0, len(users))
			for _, u := range users {
				n, _ := st.CountSitesForUser(ctx, u.Name)
				options = append(options, choice{Value: u.Name, Label: u.Name,
					Note: pluralSites(n)})
			}
			owner, err := p.pick("Which tenant?", options, users[0].Name)
			if err != nil {
				return res, err
			}
			res.Grant.User = owner
		}
	case state.ScopeSite:
		if res.Grant.Site == "" {
			sites, err := st.ListSites(ctx, state.SiteFilter{})
			if err != nil {
				return res, err
			}
			if len(sites) == 0 {
				return res, rlerr.Preconditionf("there are no sites yet")
			}
			options := make([]choice, 0, len(sites))
			for _, s := range sites {
				options = append(options, choice{Value: s.Domain, Label: s.Domain,
					Note: "owned by " + s.Owner})
			}
			site, err := p.pick("Which site?", options, sites[0].Domain)
			if err != nil {
				return res, err
			}
			res.Grant.Site = site
		}
		// The honest warning, at the point the decision is made rather than buried
		// in a document nobody reads.
		p.note("Site scope confines sftp, rsync and git to that directory. It runs as the")
		p.note("site owner's UID, so it is a blast-radius boundary rather than a kernel one.")
		p.note("For real isolation, give the site its own user instead.")
	}

	if res.Label == "" {
		label, err := p.ask(`Label, so you recognise it in two years ("Ali MacBook"):`, "", validate.Label)
		if err != nil {
			return res, err
		}
		res.Label = label
	}

	if len(res.KeyRefs) == 0 {
		source, err := p.pick("Where is the public key?", []choice{
			{Value: "path", Label: "A file on this server"},
			{Value: "github", Label: "Fetch from GitHub", Note: "by username"},
			{Value: "paste", Label: "Paste it"},
		}, "path")
		if err != nil {
			return res, err
		}
		switch source {
		case "path":
			ref, err := p.ask("Path to the .pub file:", defaultKeyPath(), nil)
			if err != nil {
				return res, err
			}
			res.KeyRefs = append(res.KeyRefs, ref)
		case "github":
			user, err := p.ask("GitHub username:", "", nil)
			if err != nil {
				return res, err
			}
			res.KeyRefs = append(res.KeyRefs, "github:"+user)
		case "paste":
			line, err := p.ask("Paste the key line:", "", func(s string) error {
				_, _, err := sshkey.Parse(s, Policy(g))
				return err
			})
			if err != nil {
				return res, err
			}
			res.KeyRefs = append(res.KeyRefs, "literal:"+line)
		}
	}

	if len(res.Grant.FromCIDR) == 0 {
		restrict, err := p.confirm("Restrict it to particular source addresses?", res.Grant.Scope == state.ScopeSite)
		if err != nil {
			return res, err
		}
		if restrict {
			list, err := p.ask("Addresses or CIDR blocks, comma separated:", "", func(s string) error {
				_, err := validate.CIDRList(s)
				return err
			})
			if err != nil {
				return res, err
			}
			res.Grant.FromCIDR, _ = validate.CIDRList(list)
		}
	}

	if res.Grant.ExpiresAt.IsZero() {
		def := ""
		if res.Grant.Scope == state.ScopeSite {
			// A contractor's access should end by default; a permanent grant is the
			// deliberate choice, not the accident.
			def = "90d"
		}
		answer, err := p.ask("Expiry (a date, a duration such as 90d, or blank for never):", def, func(s string) error {
			if s == "" {
				return nil
			}
			_, err := validate.ExpiryTime(s, time.Now())
			return err
		})
		if err != nil {
			return res, err
		}
		if answer != "" {
			at, _ := validate.ExpiryTime(answer, time.Now())
			res.Grant.ExpiresAt = at
		}
	}

	res.Grant.ShellWrapper = g.Cfg.Paths.ShellWrapper
	if err := sshkey.ResolveScope(&res.Grant, nil); err != nil && res.Grant.Scope != state.ScopeSite {
		return res, err
	}

	// The preview is the point of the wizard: what this key will be able to reach,
	// in the same words `key test` uses, before it exists.
	argv := []string{"ratline", "key", "add", "--scope", res.Grant.Scope, "--label", quoteIfNeeded(res.Label)}
	fields := [][2]string{
		{"scope", res.Grant.Scope},
		{"label", res.Label},
	}
	if res.Grant.User != "" && res.Grant.Scope == state.ScopeUser {
		argv = append(argv, "--user", res.Grant.User)
		fields = append(fields, [2]string{"tenant", res.Grant.User})
	}
	if res.Grant.Site != "" {
		argv = append(argv, "--site", res.Grant.Site)
		fields = append(fields, [2]string{"site", res.Grant.Site})
	}
	for _, ref := range res.KeyRefs {
		switch {
		case strings.HasPrefix(ref, "github:"):
			argv = append(argv, "--from-github", strings.TrimPrefix(ref, "github:"))
		case strings.HasPrefix(ref, "literal:"):
			argv = append(argv, "--key", "-")
		default:
			argv = append(argv, "--key", ref)
		}
	}
	if len(res.Grant.FromCIDR) > 0 {
		argv = append(argv, "--from", strings.Join(res.Grant.FromCIDR, ","))
		fields = append(fields, [2]string{"source", strings.Join(res.Grant.FromCIDR, ", ") + " only"})
	} else {
		fields = append(fields, [2]string{"source", "any address"})
	}
	if !res.Grant.ExpiresAt.IsZero() {
		argv = append(argv, "--expires", res.Grant.ExpiresAt.Format("2006-01-02"))
		fields = append(fields, [2]string{"expires", res.Grant.ExpiresAt.Format("2006-01-02")})
	} else {
		fields = append(fields, [2]string{"expires", "never"})
	}
	switch res.Grant.Scope {
	case state.ScopeGlobal:
		fields = append(fields, [2]string{"grants", "a shell as the administrator, and permission to run ratline"})
	case state.ScopeUser:
		fields = append(fields, [2]string{"grants", "a shell as " + res.Grant.User + ", and every site that user owns"})
	case state.ScopeSite:
		fields = append(fields, [2]string{"grants", "sftp, rsync and git inside " + res.Grant.Site + " — no shell"})
		fields = append(fields, [2]string{"note", "not a kernel boundary; see SECURITY.md"})
	}

	action, err := p.summary("Ready to grant", fields, argv)
	if err != nil {
		return res, err
	}
	if action != actionRun {
		return res, ErrCancelled
	}
	res.Argv = argv
	g.Argv = argv
	return res, nil
}

// Policy exposes the configured key policy to the wizard's inline validator.
func Policy(g *Globals) sshkey.Policy {
	return sshkey.Policy{
		MinRSABits:         g.Cfg.SSH.MinRSABits,
		WarnRSABits:        g.Cfg.SSH.WarnRSABits,
		AllowedAlgorithms:  g.Cfg.SSH.AllowedAlgorithms,
		RejectedAlgorithms: g.Cfg.SSH.RejectedAlgorithms,
		MaxLineBytes:       g.Cfg.SSH.MaxKeyLineBytes,
	}
}

// wizardCertIssue collects what an issuance needs, and runs the preflight live so
// the operator sees whether it will work before committing an attempt.
func wizardCertIssue(g *Globals, ctx context.Context, mgr *tls.Manager, opts tls.IssueOptions) (tls.IssueOptions, error) {
	p := newPrompter(g)
	st, err := g.Store(ctx)
	if err != nil {
		return opts, err
	}
	p.heading("Issue a certificate")

	if opts.Domain == "" {
		sites, err := st.ListSites(ctx, state.SiteFilter{})
		if err != nil {
			return opts, err
		}
		if len(sites) == 0 {
			return opts, rlerr.Preconditionf("there are no sites yet").
				WithHint("create one first: ratline site add <domain> --user <user> --runtime static")
		}
		options := make([]choice, 0, len(sites))
		for _, s := range sites {
			note := "no certificate"
			if cert, err := st.CertificateForSite(ctx, s.Domain); err == nil {
				note = cert.Source + ", " + pluralDays(cert.DaysRemaining(time.Now()))
			}
			options = append(options, choice{Value: s.Domain, Label: s.Domain, Note: note})
		}
		domain, err := p.pick("Which site?", options, sites[0].Domain)
		if err != nil {
			return opts, err
		}
		opts.Domain = domain
	}

	if opts.Email == "" && g.Cfg.ACME.Email == "" {
		email, err := p.ask("ACME contact address (expiry warnings go here):", "", validate.Email)
		if err != nil {
			return opts, err
		}
		opts.Email = email
	}

	if opts.Challenge == "" || opts.Challenge == "http" {
		if validate.IsWildcard(opts.Domain) {
			p.note("A wildcard requires DNS-01: HTTP-01 proves control of one hostname.")
			opts.Challenge = "dns"
		} else {
			opts.Challenge = "http"
		}
	}
	if opts.Challenge == "dns" && opts.DNSProvider == "" {
		provider, err := p.ask("certbot DNS plugin (cloudflare, route53, digitalocean, …):", "cloudflare", nil)
		if err != nil {
			return opts, err
		}
		opts.DNSProvider = provider
		creds, err := p.ask("Credentials file (must be 0600):",
			g.Cfg.Paths.DNSCredentials+"/"+provider+".ini", nil)
		if err != nil {
			return opts, err
		}
		opts.DNSCredentials = creds
	}

	// Run the preflight now, so the operator decides with the facts in front of
	// them rather than after spending an attempt.
	if err := mgr.Resolve(&opts); err != nil {
		return opts, err
	}
	names, err := mgr.Names(ctx, &opts)
	if err != nil {
		return opts, err
	}
	p.note("Checking %s …", strings.Join(names, ", "))
	checks, err := mgr.Preflight(ctx, &opts, names)
	if err != nil {
		return opts, err
	}
	g.printPreflight(checks)

	if perr := tls.PreflightError(opts.Domain, checks); perr != nil {
		p.note("Preflight found problems, so a real attempt would probably fail.")
		what, err := p.pick("What now?", []choice{
			{Value: "dry", Label: "Dry run", Note: "validate fully, spend nothing"},
			{Value: "staging", Label: "Use staging", Note: "a real but untrusted certificate"},
			{Value: "force", Label: "Try anyway", Note: "spends a rate-limit attempt"},
			{Value: "cancel", Label: "Cancel"},
		}, "dry")
		if err != nil {
			return opts, err
		}
		switch what {
		case "dry":
			opts.CertbotDryRun = true
		case "staging":
			opts.Staging = true
		case "force":
			opts.Force = true
		default:
			return opts, ErrCancelled
		}
		opts.SkipPreflight = true
	}

	argv := []string{"ratline", "cert", "issue", opts.Domain}
	fields := [][2]string{
		{"names", strings.Join(names, ", ")},
		{"challenge", opts.Challenge},
	}
	if opts.Email != "" {
		argv = append(argv, "--email", opts.Email)
		fields = append(fields, [2]string{"contact", opts.Email})
	}
	if opts.Challenge == "dns" {
		argv = append(argv, "--challenge", "dns", "--dns-provider", opts.DNSProvider,
			"--dns-credentials", opts.DNSCredentials)
		fields = append(fields, [2]string{"dns provider", opts.DNSProvider})
	}
	if opts.Staging {
		argv = append(argv, "--staging")
		fields = append(fields, [2]string{"endpoint", "staging — browsers will reject the result"})
	}
	if opts.CertbotDryRun {
		argv = append(argv, "--dry-run")
		fields = append(fields, [2]string{"mode", "dry run — nothing will be issued"})
	}
	if opts.Force {
		argv = append(argv, "--force")
	}

	action, err := p.summary("Ready to request", fields, argv)
	if err != nil {
		return opts, err
	}
	if action != actionRun {
		return opts, ErrCancelled
	}
	g.Argv = argv
	return opts, nil
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func pluralSites(n int) string {
	if n == 1 {
		return "1 site"
	}
	return itoaFast(n) + " sites"
}

func pluralDays(n int) string {
	if n == 1 {
		return "1 day left"
	}
	return itoaFast(n) + " days left"
}

func itoaFast(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
