package state

import (
	"strings"
	"time"
)

// User is a tenant sandbox.
type User struct {
	Name          string    `json:"name"`
	UID           int       `json:"uid"`
	GID           int       `json:"gid"`
	Home          string    `json:"home"`
	Shell         string    `json:"shell"`
	Comment       string    `json:"comment,omitempty"`
	Quota         string    `json:"quota,omitempty"`
	MemoryMax     string    `json:"memory_max,omitempty"`
	SFTPOnly      bool      `json:"sftp_only"`
	PasswordLogin bool      `json:"password_login"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by,omitempty"`
}

// Site is one domain owned by one user.
type Site struct {
	Domain  string   `json:"domain"`
	Owner   string   `json:"user"`
	Runtime string   `json:"runtime"`
	Slug    string   `json:"slug"`
	Enabled bool     `json:"enabled"`
	Aliases []string `json:"aliases,omitempty"`

	// static
	DocRoot   string `json:"doc_root,omitempty"`
	SPA       bool   `json:"spa,omitempty"`
	IndexFile string `json:"index_file,omitempty"`

	// node
	Entry          string `json:"entry,omitempty"`
	NodeVersion    string `json:"node_version,omitempty"`
	PackageManager string `json:"package_manager,omitempty"`
	Listen         string `json:"listen,omitempty"`
	Port           int    `json:"port,omitempty"`
	Instances      int    `json:"instances,omitempty"`

	// python
	AppModule     string `json:"app_module,omitempty"`
	PythonVersion string `json:"python_version,omitempty"`
	ASGI          bool   `json:"asgi,omitempty"`
	AppServer     string `json:"app_server,omitempty"`
	Workers       int    `json:"workers,omitempty"`
	Requirements  string `json:"requirements,omitempty"`
	ManagePy      string `json:"manage_py,omitempty"`
	StaticURL     string `json:"static_url,omitempty"`
	StaticDir     string `json:"static_dir,omitempty"`

	// shared
	StartCommand   string `json:"start_command,omitempty"`
	InstallCommand string `json:"install_command,omitempty"`
	BuildCommand   string `json:"build_command,omitempty"`
	BuildOutput    string `json:"build_output,omitempty"`
	PublicDir      string `json:"public_dir,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Branch         string `json:"branch,omitempty"`

	MemoryMax         string   `json:"memory_max,omitempty"`
	CPUQuota          string   `json:"cpu_quota,omitempty"`
	ClientMaxBodySize string   `json:"client_max_body_size,omitempty"`
	WWWRedirect       string   `json:"www_redirect,omitempty"`
	HSTS              bool     `json:"hsts"`
	Relaxed           []string `json:"relaxed,omitempty"`

	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
	LastDeployAt time.Time `json:"last_deploy_at,omitempty"`
}

// Dynamic reports whether the site has an application process behind nginx.
func (s *Site) Dynamic() bool { return s.Runtime == "node" || s.Runtime == "python" }

// ServerNames is the domain plus every alias, which is what nginx needs.
func (s *Site) ServerNames() []string {
	return append([]string{s.Domain}, s.Aliases...)
}

// Key scopes.
const (
	ScopeGlobal = "global"
	ScopeUser   = "user"
	ScopeSite   = "site"
)

// Key is one authorized SSH public key.
type Key struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Fingerprint string    `json:"fingerprint"`
	Algorithm   string    `json:"algorithm"`
	Bits        int       `json:"bits"`
	Blob        string    `json:"blob"`
	Comment     string    `json:"comment,omitempty"`
	Scope       string    `json:"scope"`
	Owner       string    `json:"user,omitempty"`
	Site        string    `json:"site,omitempty"`
	Options     string    `json:"options,omitempty"`
	Source      string    `json:"source"`
	AllowShell  bool      `json:"allow_shell"`
	SFTPOnly    bool      `json:"sftp_only"`
	FromCIDR    []string  `json:"from,omitempty"`
	Command     string    `json:"command,omitempty"`
	AddedAt     time.Time `json:"added_at"`
	AddedBy     string    `json:"added_by,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	LastUsedIP  string    `json:"last_used_ip,omitempty"`
	RevokedAt   time.Time `json:"revoked_at,omitempty"`
}

// Expired reports whether the key is past its expiry.
func (k *Key) Expired(at time.Time) bool {
	return !k.ExpiresAt.IsZero() && k.ExpiresAt.Before(at)
}

// Target names what the key reaches, for listings.
func (k *Key) Target() string {
	switch k.Scope {
	case ScopeSite:
		return k.Site
	case ScopeUser:
		return k.Owner
	default:
		return "server"
	}
}

// Certificate sources.
const (
	CertSourceLetsEncrypt = "letsencrypt"
	CertSourceStaging     = "staging"
	CertSourceImported    = "imported"
	CertSourceSelfSigned  = "selfsigned"
)

// Certificate is one certificate ratline knows about, including ones issued by
// hand outside the tool and found by a scan.
type Certificate struct {
	Name                string    `json:"name"`
	Lineage             string    `json:"lineage,omitempty"`
	Source              string    `json:"source"`
	Issuer              string    `json:"issuer,omitempty"`
	Serial              string    `json:"serial,omitempty"`
	Fingerprint         string    `json:"fingerprint,omitempty"`
	KeyType             string    `json:"key_type,omitempty"`
	NotBefore           time.Time `json:"not_before,omitempty"`
	NotAfter            time.Time `json:"not_after,omitempty"`
	Challenge           string    `json:"challenge,omitempty"`
	DNSProvider         string    `json:"dns_provider,omitempty"`
	AutoRenew           bool      `json:"auto_renew"`
	CertPath            string    `json:"cert_path,omitempty"`
	KeyPath             string    `json:"key_path,omitempty"`
	ChainPath           string    `json:"chain_path,omitempty"`
	LastRenewalAt       time.Time `json:"last_renewal_at,omitempty"`
	LastRenewalStatus   string    `json:"last_renewal_status,omitempty"`
	LastRenewalError    string    `json:"last_renewal_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	SANs     []string `json:"sans,omitempty"`
	Attached []string `json:"attached_sites,omitempty"`
}

// Trusted reports whether a browser would accept this certificate. Staging and
// self-signed certificates exist to unblock work, not to be shipped.
func (c *Certificate) Trusted() bool {
	return c.Source == CertSourceLetsEncrypt || c.Source == CertSourceImported
}

// DaysRemaining reports whole days until expiry; negative once expired.
func (c *Certificate) DaysRemaining(at time.Time) int {
	if c.NotAfter.IsZero() {
		return 0
	}
	return int(c.NotAfter.Sub(at).Hours() / 24)
}

// Covers reports whether the certificate's SANs include a name, honouring one
// level of wildcard exactly as a TLS client would.
func (c *Certificate) Covers(name string) bool {
	name = strings.ToLower(name)
	for _, san := range c.SANs {
		san = strings.ToLower(san)
		if san == name {
			return true
		}
		if strings.HasPrefix(san, "*.") {
			base := san[2:]
			host, rest, found := strings.Cut(name, ".")
			if found && rest == base && host != "" && !strings.Contains(host, ".") {
				return true
			}
		}
	}
	return false
}

// ACMEAttempt records one issuance attempt for rate-limit budgeting.
type ACMEAttempt struct {
	ID               int64     `json:"id"`
	RegisteredDomain string    `json:"registered_domain"`
	Domain           string    `json:"domain"`
	SANSet           string    `json:"san_set,omitempty"`
	AttemptedAt      time.Time `json:"attempted_at"`
	Outcome          string    `json:"outcome"`
	ErrorClass       string    `json:"error_class,omitempty"`
	Staging          bool      `json:"staging"`
}

// ACME attempt outcomes.
const (
	ACMESuccess = "success"
	ACMEFailure = "failure"
)

// Port is a TCP port allocated to a site.
type Port struct {
	Port        int       `json:"port"`
	Domain      string    `json:"domain"`
	AllocatedAt time.Time `json:"allocated_at"`
}

// Deployment is one `site deploy` run.
type Deployment struct {
	ID         int64     `json:"id"`
	Domain     string    `json:"domain"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	GitSHA     string    `json:"git_sha,omitempty"`
	Steps      []string  `json:"steps,omitempty"`
	OK         bool      `json:"ok"`
	Health     string    `json:"health,omitempty"`
	RolledBack bool      `json:"rolled_back"`
	Error      string    `json:"error,omitempty"`
}

// Event is one mutating invocation.
type Event struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Command    string    `json:"command"`
	Argv       string    `json:"argv,omitempty"`
	UID        int       `json:"uid"`
	SudoUser   string    `json:"sudo_user,omitempty"`
	Target     string    `json:"target,omitempty"`
	Result     string    `json:"result,omitempty"`
	ExitCode   int       `json:"exit_code"`
	DurationMS int64     `json:"duration_ms"`
	Detail     string    `json:"detail,omitempty"`
}

// splitList and joinList store small string lists in one column. A separate
// table would be more correct, but these are never queried individually.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinList(v []string) string { return strings.Join(v, ",") }
