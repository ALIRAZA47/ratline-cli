package diag

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// Env is everything a check set needs. Assembled once by the caller so a check is
// a closure over it rather than a method on a manager it has to reach through.
type Env struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	OS     system.OSInfo

	Site  *site.Manager
	Nginx *nginx.Manager
	Unit  *unit.Manager
	TLS   *tls.Manager
	Keys  *sshkey.Manager

	// ProbeTimeout bounds every network operation a check performs: the DNS lookup,
	// the request to the application, the request through nginx, the TLS handshake.
	//
	// One knob rather than a constant per call site, because the thing that matters
	// is how long the whole diagnosis can take. `troubleshoot server` runs the nginx
	// and ssh lists as well as touching every site, and a dozen independent
	// five-second waits turns a diagnosis into a minute of silence on exactly the
	// broken host where somebody is waiting for it. Zero means the default.
	ProbeTimeout time.Duration

	// Resolver is used for the DNS check. Zero means the system resolver; a test
	// sets it to avoid depending on the network.
	Resolver *net.Resolver
}

// defaultProbeTimeout is deliberately shorter than the health-check timeout used
// when starting a site. There, waiting is the point — an application may legitimately
// take twenty seconds to come up. Here the question is whether something answers
// *now*, and a slow answer is itself the finding.
const defaultProbeTimeout = 3 * time.Second

func (e *Env) probeTimeout() time.Duration {
	if e != nil && e.ProbeTimeout > 0 {
		return e.ProbeTimeout
	}
	return defaultProbeTimeout
}

// probeContext derives the bounded context a network check should use.
func (e *Env) probeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, e.probeTimeout())
}

func (e *Env) resolver() *net.Resolver {
	if e != nil && e.Resolver != nil {
		return e.Resolver
	}
	return net.DefaultResolver
}

// Kind is what a diagnosis is about.
type Kind string

// The subjects that can be diagnosed. Each has its own check list; `Server` is the
// default when nothing is named.
const (
	KindServer Kind = "server"
	KindSite   Kind = "site"
	KindUser   Kind = "user"
	KindKey    Kind = "key"
	KindCert   Kind = "certificate"
	KindNginx  Kind = "nginx"
	KindSSH    Kind = "ssh"
)

// Kinds is every diagnosable subject, in the order they are offered.
var Kinds = []Kind{KindServer, KindSite, KindUser, KindKey, KindCert, KindNginx, KindSSH}

// Subject is a resolved thing to diagnose.
type Subject struct {
	Kind Kind
	Name string

	// Exactly one of these is set for the resource kinds.
	Site *state.Site
	User *state.User
	Key  *state.Key
	Cert *state.Certificate
}

// subsystems are the fixed names, which are not resources and cannot collide with
// one because a domain has a dot and a username may not be one of these.
var subsystems = map[string]Kind{
	"server": KindServer,
	"host":   KindServer,
	"nginx":  KindNginx,
	"web":    KindNginx,
	"ssh":    KindSSH,
	"sshd":   KindSSH,
}

// Resolve works out what an operator meant by one argument.
//
// Auto-detection rather than a required `--kind`, because the argument is almost
// always unambiguous — `app.example.com` is a site, `acme` is a tenant,
// `SHA256:…` is a key — and making somebody say which kind of thing their own
// domain is would be a worse tool. Where it genuinely is ambiguous, a name that is
// both a tenant and a certificate lineage, the ambiguity is reported with both
// disambiguating commands rather than guessed.
func Resolve(ctx context.Context, env *Env, arg string) (*Subject, error) {
	arg = strings.TrimSpace(arg)
	if env == nil || env.State == nil {
		// Only the subsystems can be named without a database, and the server is the
		// default — so a state store that would not open is itself the finding.
		if k, ok := subsystems[strings.ToLower(arg)]; ok || arg == "" {
			if arg == "" {
				k = KindServer
			}
			return &Subject{Kind: k, Name: orDefault(strings.ToLower(arg), "this server")}, nil
		}
		return nil, rlerr.Preconditionf("the state database could not be opened, so %q "+
			"cannot be looked up", arg).
			WithHint("ratline troubleshoot server checks the database itself")
	}
	if arg == "" {
		return &Subject{Kind: KindServer, Name: hostnameOf(ctx, env)}, nil
	}
	if k, ok := subsystems[strings.ToLower(arg)]; ok {
		return &Subject{Kind: k, Name: strings.ToLower(arg)}, nil
	}

	// A fingerprint is unmistakable, and checking it first avoids a pointless site
	// lookup on a string with a colon in it.
	if strings.HasPrefix(arg, "SHA256:") || strings.HasPrefix(arg, "MD5:") {
		return resolveKey(ctx, env, arg)
	}

	var found []*Subject
	if s, err := env.State.FindSiteByName(ctx, arg); err == nil && s != nil {
		found = append(found, &Subject{Kind: KindSite, Name: s.Domain, Site: s})
	}
	if u, err := env.State.GetUser(ctx, arg); err == nil && u != nil {
		found = append(found, &Subject{Kind: KindUser, Name: u.Name, User: u})
	}
	if c, err := env.State.GetCertificate(ctx, arg); err == nil && c != nil {
		// Only when it is not already the site of the same name: `cert issue` names a
		// certificate after its primary domain, so the two collide constantly and the
		// site is the more useful answer — a certificate is one of the site's checks.
		if len(found) == 0 || found[0].Kind != KindSite {
			found = append(found, &Subject{Kind: KindCert, Name: c.Name, Cert: c})
		}
	}
	if keys, err := env.State.FindKeys(ctx, arg, state.KeyFilter{}); err == nil && len(keys) == 1 {
		found = append(found, &Subject{Kind: KindKey, Name: keys[0].Fingerprint, Key: keys[0]})
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, unknownSubject(ctx, env, arg)
	default:
		kinds := make([]string, 0, len(found))
		for _, f := range found {
			kinds = append(kinds, string(f.Kind))
		}
		return nil, rlerr.Usagef("%q is both a %s", arg, strings.Join(kinds, " and a ")).
			WithHint("say which: ratline troubleshoot %s --kind %s", arg, found[0].Kind)
	}
}

// ResolveKind resolves an argument that has been narrowed by --kind.
func ResolveKind(ctx context.Context, env *Env, arg string, kind Kind) (*Subject, error) {
	switch kind {
	case KindServer, KindNginx, KindSSH:
		name := arg
		if name == "" {
			name = string(kind)
		}
		return &Subject{Kind: kind, Name: name}, nil
	case KindSite:
		s, err := env.State.FindSiteByName(ctx, arg)
		if err != nil {
			return nil, err
		}
		return &Subject{Kind: KindSite, Name: s.Domain, Site: s}, nil
	case KindUser:
		u, err := env.State.GetUser(ctx, arg)
		if err != nil {
			return nil, err
		}
		return &Subject{Kind: KindUser, Name: u.Name, User: u}, nil
	case KindCert:
		c, err := env.State.GetCertificate(ctx, arg)
		if err != nil {
			return nil, err
		}
		return &Subject{Kind: KindCert, Name: c.Name, Cert: c}, nil
	case KindKey:
		return resolveKey(ctx, env, arg)
	default:
		return nil, rlerr.Usagef("unknown subject kind %q", kind).
			WithHint("choose one of: %s", strings.Join(kindNames(), ", "))
	}
}

func kindNames() []string {
	out := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, string(k))
	}
	return out
}

// resolveKey finds one key by fingerprint, label or id.
func resolveKey(ctx context.Context, env *Env, arg string) (*Subject, error) {
	keys, err := env.State.FindKeys(ctx, arg, state.KeyFilter{IncludeRevoked: true})
	if err != nil {
		return nil, err
	}
	switch len(keys) {
	case 1:
		return &Subject{Kind: KindKey, Name: keys[0].Fingerprint, Key: keys[0]}, nil
	case 0:
		return nil, rlerr.Preconditionf("no key matches %q", arg).
			WithHint("ratline key list shows the fingerprints and labels")
	default:
		return nil, rlerr.Usagef("%d keys match %q", len(keys), arg).
			WithHint("use the full fingerprint; ratline key list shows them")
	}
}

// unknownSubject explains what the argument could have been, using what is actually
// on this server rather than a generic list.
//
// A diagnostic that cannot find its subject is the worst moment to be unhelpful:
// the operator already knows something is wrong and now has two problems.
func unknownSubject(ctx context.Context, env *Env, arg string) error {
	e := rlerr.Preconditionf("nothing on this server is called %q", arg)

	if near := nearestName(ctx, env, arg); near != "" {
		return e.WithHint("did you mean %q? Or name a subsystem: %s",
			near, strings.Join(kindNames(), ", "))
	}
	// Looks like a domain but is not one ratline knows: almost always a site that
	// was never created, or a typo in a domain that is easy to mistype.
	if strings.Contains(arg, ".") {
		return e.WithHint("ratline site list shows the domains it manages; " +
			"a site that does not exist yet is created with 'ratline site add'")
	}
	return e.WithHint("a domain, a tenant, a key fingerprint, a certificate, "+
		"or one of: %s", strings.Join(kindNames(), ", "))
}

// nearestName finds a plausible correction among the names on this server.
func nearestName(ctx context.Context, env *Env, arg string) string {
	arg = strings.ToLower(arg)
	var names []string
	if sites, err := env.State.ListSites(ctx, state.SiteFilter{}); err == nil {
		for _, s := range sites {
			names = append(names, s.Domain)
		}
	}
	if users, err := env.State.ListUsers(ctx); err == nil {
		for _, u := range users {
			names = append(names, u.Name)
		}
	}
	if certs, err := env.State.ListCertificates(ctx); err == nil {
		for _, c := range certs {
			names = append(names, c.Name)
		}
	}
	// Prefix and containment rather than an edit distance: the realistic mistakes
	// here are a missing subdomain, a wrong TLD and a truncated name, all of which
	// this catches and a distance threshold tuned for single-character typos does
	// not.
	for _, n := range names {
		l := strings.ToLower(n)
		if strings.HasPrefix(l, arg) || strings.HasPrefix(arg, l) {
			return n
		}
	}
	for _, n := range names {
		l := strings.ToLower(n)
		if strings.Contains(l, arg) || strings.Contains(arg, l) {
			return n
		}
	}
	return ""
}

// hostnameOf is the server's name, for the report header.
func hostnameOf(ctx context.Context, env *Env) string {
	if env.Cfg != nil && env.Cfg.Server.Hostname != "" {
		return env.Cfg.Server.Hostname
	}
	if env.State != nil {
		if h, err := env.State.GetServerValue(ctx, "hostname"); err == nil && h != "" {
			return h
		}
	}
	return "this server"
}

// Diagnose runs the check list for a subject.
func Diagnose(ctx context.Context, env *Env, s *Subject) (*Report, error) {
	switch s.Kind {
	case KindSite:
		return Run(ctx, string(s.Kind), s.Name, siteSummary(s.Site), SiteChecks(env, s.Site)), nil
	case KindUser:
		return Run(ctx, string(s.Kind), s.Name, userSummary(s.User), UserChecks(env, s.User)), nil
	case KindKey:
		return Run(ctx, string(s.Kind), s.Name, keySummary(s.Key), KeyChecks(env, s.Key)), nil
	case KindCert:
		return Run(ctx, string(s.Kind), s.Name, certSummary(s.Cert), CertChecks(env, s.Cert)), nil
	case KindNginx:
		return Run(ctx, string(s.Kind), "", "the web server and its generated configuration",
			NginxChecks(env)), nil
	case KindSSH:
		return Run(ctx, string(s.Kind), "", "sshd, its drop-in, and the managed key blocks",
			SSHChecks(env)), nil
	case KindServer:
		return Run(ctx, string(s.Kind), s.Name, "the host and everything ratline needs from it",
			ServerChecks(env)), nil
	default:
		return nil, rlerr.Usagef("cannot diagnose a %q", s.Kind)
	}
}

// Nil-safe readers for everything the checks look up.
//
// A check must never panic: it runs on a server where something is already wrong,
// and a crash there is worse than no diagnosis at all. Rather than every check
// guarding its own dependency and handling its own query error — twenty chances to
// forget — the reads go through these, which return (zero, false) when the store is
// absent or the query fails. A check then has one branch: could I look, or not.

func (e *Env) users(ctx context.Context) ([]*state.User, bool) {
	if e == nil || e.State == nil {
		return nil, false
	}
	users, err := e.State.ListUsers(ctx)
	if err != nil {
		return nil, false
	}
	return users, true
}

func (e *Env) sites(ctx context.Context, f state.SiteFilter) ([]*state.Site, bool) {
	if e == nil || e.State == nil {
		return nil, false
	}
	sites, err := e.State.ListSites(ctx, f)
	if err != nil {
		return nil, false
	}
	return sites, true
}

func (e *Env) keys(ctx context.Context, f state.KeyFilter) ([]*state.Key, bool) {
	if e == nil || e.State == nil {
		return nil, false
	}
	keys, err := e.State.ListKeys(ctx, f)
	if err != nil {
		return nil, false
	}
	return keys, true
}

func (e *Env) certs(ctx context.Context) ([]*state.Certificate, bool) {
	if e == nil || e.State == nil {
		return nil, false
	}
	certs, err := e.State.ListCertificates(ctx)
	if err != nil {
		return nil, false
	}
	return certs, true
}

func (e *Env) site(ctx context.Context, domain string) (*state.Site, bool) {
	if e == nil || e.State == nil {
		return nil, false
	}
	s, err := e.State.GetSite(ctx, domain)
	if err != nil || s == nil {
		return nil, false
	}
	return s, true
}

func (e *Env) keysInScope(ctx context.Context, scope, owner, site string) (int, bool) {
	if e == nil || e.State == nil {
		return 0, false
	}
	n, err := e.State.CountKeysInScope(ctx, scope, owner, site)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (e *Env) schemaVersion(ctx context.Context) (int, bool) {
	if e == nil || e.State == nil {
		return 0, false
	}
	v, err := e.State.SchemaVersion(ctx)
	if err != nil {
		return 0, false
	}
	return v, true
}

// unitStatus is nil-safe in the same way, and folds "could not query" into one
// boolean so a check does not have to distinguish an error from a nil status.
func (e *Env) unitStatus(ctx context.Context, s *state.Site) (*unit.Status, bool) {
	if e == nil || e.Unit == nil {
		return nil, false
	}
	st, err := e.Unit.Status(ctx, s)
	if err != nil || st == nil {
		return nil, false
	}
	return st, true
}
