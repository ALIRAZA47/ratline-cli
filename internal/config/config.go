// Package config defines /etc/ratline/config.yaml.
//
// The defaults live in defaults.yaml, embedded in the binary. That file is both
// the source of every default value and the commented reference an operator
// reads, which keeps the two from drifting apart.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// DefaultPath is where ratline looks unless --config says otherwise.
const DefaultPath = "/etc/ratline/config.yaml"

// SchemaVersion is bumped when a change needs a migration rather than a merge.
const SchemaVersion = 1

// Duration is a time.Duration that reads as "30s" in YAML.
type Duration time.Duration

func (d Duration) D() time.Duration          { return time.Duration(d) }
func (d Duration) String() string            { return time.Duration(d).String() }
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: a duration must be a string such as \"30s\"", n.Line)
	}
	v, err := validate.Duration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// Config is the whole file.
type Config struct {
	Version  int      `yaml:"version"`
	Server   Server   `yaml:"server"`
	Paths    Paths    `yaml:"paths"`
	Defaults Defaults `yaml:"defaults"`
	Users    Users    `yaml:"users"`
	SSH      SSH      `yaml:"ssh"`
	Nginx    Nginx    `yaml:"nginx"`
	Runtimes Runtimes `yaml:"runtimes"`
	ACME     ACME     `yaml:"acme"`
	Ports    Ports    `yaml:"ports"`
	Logging  Logging  `yaml:"logging"`
	Features Features `yaml:"features"`

	Databases Databases `yaml:"databases"`

	// SourcePath and Loaded describe where this config came from. Loaded is
	// false when no file exists and the built-in defaults are in use.
	SourcePath string `yaml:"-"`
	Loaded     bool   `yaml:"-"`
}

// Server records facts about this host that are expensive to detect.
type Server struct {
	Hostname   string   `yaml:"hostname"`
	PublicIPv4 []string `yaml:"public_ipv4"`
	PublicIPv6 []string `yaml:"public_ipv6"`
	// AdminUser holds global-scope SSH keys. Empty means the account that ran
	// `ratline init`.
	AdminUser string `yaml:"admin_user"`
}

// Paths is every filesystem location ratline owns or reads.
type Paths struct {
	StateDB             string `yaml:"state_db"`
	AuditLog            string `yaml:"audit_log"`
	Lock                string `yaml:"lock"`
	RunDir              string `yaml:"run_dir"`
	HomeBase            string `yaml:"home_base"`
	NginxSitesAvailable string `yaml:"nginx_sites_available"`
	NginxSitesEnabled   string `yaml:"nginx_sites_enabled"`
	NginxSnippets       string `yaml:"nginx_snippets"`
	NginxCustom         string `yaml:"nginx_custom"`
	SystemdDir          string `yaml:"systemd_dir"`
	LogrotateDir        string `yaml:"logrotate_dir"`
	ACMEWebroot         string `yaml:"acme_webroot"`
	LetsEncryptDir      string `yaml:"letsencrypt_dir"`
	ImportedCerts       string `yaml:"imported_certs"`
	DNSCredentials      string `yaml:"dns_credentials"`
	// MongoURIFile holds the admin connection string. A file rather than a setting
	// in this one, held to the same 0600 rule as the DNS credentials: it is the
	// root password for every database on the server, and config.yaml is a file
	// operators paste into support tickets.
	MongoURIFile string `yaml:"mongo_uri_file"`
	SSHDir       string `yaml:"ssh_dir"`
	SSHDDropIn   string `yaml:"sshd_dropin"`
	RuntimesDir  string `yaml:"runtimes_dir"`
	ShellWrapper string `yaml:"shell_wrapper"`
	BackupDir    string `yaml:"backup_dir"`
}

// Defaults are the per-site values `site add` starts from and `site scale`
// overrides.
type Defaults struct {
	Shell             string   `yaml:"shell"`
	Umask             string   `yaml:"umask"`
	ClientMaxBodySize string   `yaml:"client_max_body_size"`
	HealthTimeout     Duration `yaml:"health_timeout"`
	LockTimeout       Duration `yaml:"lock_timeout"`
	ProxyReadTimeout  Duration `yaml:"proxy_read_timeout"`
	RestartSec        Duration `yaml:"restart_sec"`
	StopTimeout       Duration `yaml:"stop_timeout"`
	MemoryMax         string   `yaml:"memory_max"`
	MemoryHighRatio   float64  `yaml:"memory_high_ratio"`
	CPUQuota          string   `yaml:"cpu_quota"`
	TasksMax          int      `yaml:"tasks_max"`
	LimitNOFILE       int      `yaml:"limit_nofile"`
	WorkerCap         int      `yaml:"worker_cap"`
	// HSTS stays off by default: enabling it on one site can break a tenant's
	// unrelated subdomains, and that is not ours to decide.
	HSTS       bool `yaml:"hsts"`
	HSTSMaxAge int  `yaml:"hsts_max_age"`
}

// Users governs account creation.
type Users struct {
	Reserved     []string `yaml:"reserved"`
	AllowSudo    bool     `yaml:"allow_sudo"`
	QuotaEnabled bool     `yaml:"quota_enabled"`
	NginxUser    string   `yaml:"nginx_user"`
	LogGroup     string   `yaml:"log_group"`
	HomeMode     string   `yaml:"home_mode"`
	SiteMode     string   `yaml:"site_mode"`
}

// SSH is the key policy and the sshd integration.
type SSH struct {
	MinRSABits         int               `yaml:"min_rsa_bits"`
	WarnRSABits        int               `yaml:"warn_rsa_bits"`
	AllowedAlgorithms  []string          `yaml:"allowed_algorithms"`
	RejectedAlgorithms []string          `yaml:"rejected_algorithms"`
	MaxKeyLineBytes    int               `yaml:"max_key_line_bytes"`
	MaxAuthKeysBytes   int               `yaml:"max_authorized_keys_bytes"`
	AllowRootKeys      bool              `yaml:"allow_root_keys"`
	SiteScopeSFTPOnly  bool              `yaml:"site_scope_sftp_only"`
	PruneExpired       bool              `yaml:"prune_expired"`
	CommandPresets     map[string]string `yaml:"command_presets"`
	RevokedKeys        string            `yaml:"revoked_keys"`
	GlobalKeysFile     string            `yaml:"global_keys_file"`
	VerifyAfterChange  bool              `yaml:"verify_after_change"`
	UsageScanEnabled   bool              `yaml:"usage_scan_enabled"`
	KeyFetchTimeout    Duration          `yaml:"key_fetch_timeout"`
	MaxFetchedKeyBytes int               `yaml:"max_fetched_key_bytes"`
}

// Nginx describes how ratline talks to the web server.
type Nginx struct {
	ReloadTimeout Duration `yaml:"reload_timeout"`
	Gzip          bool     `yaml:"gzip"`
	Brotli        bool     `yaml:"brotli"`
	ServerTokens  bool     `yaml:"server_tokens"`
	AssetMaxAge   int      `yaml:"asset_max_age"`
}

// Runtimes pins the managed language versions.
type Runtimes struct {
	NodeDefault string `yaml:"node_default"`
	// NodeProcessManager is pm2 or direct. PM2 is the default because it is the
	// only way a Node site reloads without dropping requests.
	NodeProcessManager string   `yaml:"node_process_manager"`
	PythonDefault      string   `yaml:"python_default"`
	NodeMirror         string   `yaml:"node_mirror"`
	InstallTimeout     Duration `yaml:"install_timeout"`
	BuildTimeout       Duration `yaml:"build_timeout"`
}

// ACME is the certificate policy, including the rate-limit budget.
type ACME struct {
	Email        string `yaml:"email"`
	DirectoryURL string `yaml:"directory_url"`
	StagingURL   string `yaml:"staging_url"`
	// CABundle verifies the ACME server itself, for a private CA.
	//
	// certbot checks the directory's TLS certificate against certifi's bundled
	// roots, not the system trust store, so a private CA's root installed with
	// update-ca-certificates is not consulted. `cert issue --ca-bundle` covers one
	// issuance; this is the setting that makes *renewal* work too, which has no
	// command line to read it from. Leave it empty for a public CA.
	CABundle              string     `yaml:"ca_bundle"`
	KeyType               string     `yaml:"key_type"`
	RenewBeforeDays       int        `yaml:"renew_before_days"`
	DNSPropagationSeconds int        `yaml:"dns_propagation_seconds"`
	TOSAgreed             bool       `yaml:"tos_agreed"`
	PreflightTimeout      Duration   `yaml:"preflight_timeout"`
	IssueTimeout          Duration   `yaml:"issue_timeout"`
	RateLimits            RateLimits `yaml:"rate_limits"`
	Alerts                Alerts     `yaml:"alerts"`
}

// RateLimits mirrors the certificate authority's published limits.
//
// These are policy, not physics: Let's Encrypt has changed them before and will
// again. They live in config so an operator can follow a change without waiting
// for a ratline release.
type RateLimits struct {
	CertsPerRegisteredDomainPerWeek int `yaml:"certs_per_registered_domain_per_week"`
	DuplicateCertsPerWeek           int `yaml:"duplicate_certs_per_week"`
	FailedValidationsPerHour        int `yaml:"failed_validations_per_hour"`
	NewOrdersPer3Hours              int `yaml:"new_orders_per_3_hours"`
}

// Alerts is where renewal failures are reported.
type Alerts struct {
	WebhookURL string `yaml:"webhook_url"`
	Email      string `yaml:"email"`
	WarnDays   int    `yaml:"warn_days"`
}

// Ports is the allocation window for node sites that listen on TCP.
type Ports struct {
	RangeStart int `yaml:"range_start"`
	RangeEnd   int `yaml:"range_end"`
}

// Logging configures the logger and the audit trail.
type Logging struct {
	Level string `yaml:"level"`
	Color string `yaml:"color"` // auto, always or never
}

// Features gates work that is not finished. `ratline db` is stubbed behind one.
// Databases groups the database servers ratline can provision inside.
//
// It provisions rather than installs. A database server is a stateful thing with
// backups and a replication topology, and a tool that silently apt-gets one has made a
// decision belonging to whoever owns the data — the same reasoning that has ratline
// configure nginx and drive certbot without installing either.
type Databases struct {
	MongoDB MongoDB `yaml:"mongodb"`
}

// MongoDB is how ratline reaches the MongoDB server it manages.
type MongoDB struct {
	// DefaultRole is granted to a user created without --role. readWrite rather than
	// dbOwner: an application needs to read and write its own collections, and does not
	// need to create users or drop the database it lives in.
	DefaultRole string `yaml:"default_role"`

	// EnvKey is the variable a connection string is written to in a site's .env.
	EnvKey string `yaml:"env_key"`

	// Timeout bounds one mongosh invocation. A managed cluster behind an access list
	// does not refuse a connection, it hangs, so this is what turns that into an error
	// naming the access list rather than a command that never returns.
	Timeout Duration `yaml:"timeout"`

	// InitialCollection is created so a new database is visible to `db list`. MongoDB
	// has no createDatabase — a database exists once something is written into it — so
	// without this a freshly created one is invisible until the application writes,
	// which reads as the create having silently failed.
	InitialCollection string `yaml:"initial_collection"`
}

type Features struct {
	DBProvisioning  bool `yaml:"db_provisioning"`
	StrictIsolation bool `yaml:"strict_isolation"`
}

// Validate checks the file for values that would fail much later, at the point
// where they reach nginx or systemd.
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if c.Version != SchemaVersion {
		add("version is %d but this ratline understands %d", c.Version, SchemaVersion)
	}

	for name, p := range map[string]string{
		"paths.state_db":              c.Paths.StateDB,
		"paths.audit_log":             c.Paths.AuditLog,
		"paths.lock":                  c.Paths.Lock,
		"paths.run_dir":               c.Paths.RunDir,
		"paths.home_base":             c.Paths.HomeBase,
		"paths.nginx_sites_available": c.Paths.NginxSitesAvailable,
		"paths.nginx_sites_enabled":   c.Paths.NginxSitesEnabled,
		"paths.nginx_snippets":        c.Paths.NginxSnippets,
		"paths.nginx_custom":          c.Paths.NginxCustom,
		"paths.systemd_dir":           c.Paths.SystemdDir,
		"paths.acme_webroot":          c.Paths.ACMEWebroot,
		"paths.letsencrypt_dir":       c.Paths.LetsEncryptDir,
		"paths.imported_certs":        c.Paths.ImportedCerts,
		"paths.dns_credentials":       c.Paths.DNSCredentials,
		"paths.ssh_dir":               c.Paths.SSHDir,
		"paths.sshd_dropin":           c.Paths.SSHDDropIn,
		"paths.runtimes_dir":          c.Paths.RuntimesDir,
		"paths.shell_wrapper":         c.Paths.ShellWrapper,
		"paths.logrotate_dir":         c.Paths.LogrotateDir,
		"paths.backup_dir":            c.Paths.BackupDir,
	} {
		if p == "" {
			add("%s is empty", name)
			continue
		}
		if !filepath.IsAbs(p) {
			add("%s must be an absolute path, got %q", name, p)
		}
	}

	if !filepath.IsAbs(c.Defaults.Shell) {
		add("defaults.shell must be an absolute path, got %q", c.Defaults.Shell)
	}
	if _, err := parseOctal(c.Defaults.Umask); err != nil {
		add("defaults.umask %q is not an octal mode", c.Defaults.Umask)
	}
	if _, err := parseOctal(c.Users.HomeMode); err != nil {
		add("users.home_mode %q is not an octal mode", c.Users.HomeMode)
	}
	if _, err := parseOctal(c.Users.SiteMode); err != nil {
		add("users.site_mode %q is not an octal mode", c.Users.SiteMode)
	}
	if _, err := validate.Size(c.Defaults.MemoryMax); err != nil {
		add("defaults.memory_max %q is not a valid size", c.Defaults.MemoryMax)
	}
	if _, err := validate.Size(c.Defaults.ClientMaxBodySize); err != nil {
		add("defaults.client_max_body_size %q is not a valid size", c.Defaults.ClientMaxBodySize)
	}
	if err := validate.CPUQuota(c.Defaults.CPUQuota); err != nil {
		add("defaults.cpu_quota %q is not a valid quota", c.Defaults.CPUQuota)
	}
	if c.Defaults.MemoryHighRatio <= 0 || c.Defaults.MemoryHighRatio > 1 {
		add("defaults.memory_high_ratio must be between 0 and 1, got %v", c.Defaults.MemoryHighRatio)
	}
	if c.Defaults.WorkerCap < 1 {
		add("defaults.worker_cap must be at least 1")
	}
	if c.Defaults.TasksMax < 16 {
		add("defaults.tasks_max must be at least 16")
	}
	if c.Defaults.HealthTimeout.D() < time.Second {
		add("defaults.health_timeout must be at least 1s")
	}

	if err := validate.PortRange(c.Ports.RangeStart, c.Ports.RangeEnd); err != nil {
		add("ports: %s", err)
	}

	if c.SSH.MinRSABits < 2048 {
		add("ssh.min_rsa_bits must be at least 2048")
	}
	if c.SSH.WarnRSABits < c.SSH.MinRSABits {
		add("ssh.warn_rsa_bits (%d) must not be below ssh.min_rsa_bits (%d)", c.SSH.WarnRSABits, c.SSH.MinRSABits)
	}
	if c.SSH.MaxKeyLineBytes < 1024 {
		add("ssh.max_key_line_bytes must be at least 1024")
	}
	if c.SSH.MaxAuthKeysBytes < c.SSH.MaxKeyLineBytes {
		add("ssh.max_authorized_keys_bytes must be at least ssh.max_key_line_bytes")
	}
	if len(c.SSH.AllowedAlgorithms) == 0 {
		add("ssh.allowed_algorithms is empty, which would refuse every key")
	}

	switch c.ACME.KeyType {
	case "ecdsa", "rsa":
	default:
		add("acme.key_type must be ecdsa or rsa, got %q", c.ACME.KeyType)
	}
	if c.ACME.Email != "" {
		if err := validate.Email(c.ACME.Email); err != nil {
			add("acme.email: %s", err)
		}
	}
	if c.ACME.RenewBeforeDays < 1 || c.ACME.RenewBeforeDays > 89 {
		add("acme.renew_before_days must be between 1 and 89, got %d", c.ACME.RenewBeforeDays)
	}
	if !strings.HasPrefix(c.ACME.DirectoryURL, "https://") {
		add("acme.directory_url must be an https URL")
	}
	if !strings.HasPrefix(c.ACME.StagingURL, "https://") {
		add("acme.staging_url must be an https URL")
	}
	if c.ACME.Alerts.WebhookURL != "" && !strings.HasPrefix(c.ACME.Alerts.WebhookURL, "https://") {
		add("acme.alerts.webhook_url must be an https URL")
	}
	if c.ACME.Alerts.Email != "" {
		if err := validate.Email(c.ACME.Alerts.Email); err != nil {
			add("acme.alerts.email: %s", err)
		}
	}
	for name, v := range map[string]int{
		"certs_per_registered_domain_per_week": c.ACME.RateLimits.CertsPerRegisteredDomainPerWeek,
		"duplicate_certs_per_week":             c.ACME.RateLimits.DuplicateCertsPerWeek,
		"failed_validations_per_hour":          c.ACME.RateLimits.FailedValidationsPerHour,
		"new_orders_per_3_hours":               c.ACME.RateLimits.NewOrdersPer3Hours,
	} {
		if v < 1 {
			add("acme.rate_limits.%s must be at least 1", name)
		}
	}

	if c.Runtimes.NodeDefault != "" {
		if err := validate.NodeVersion(c.Runtimes.NodeDefault); err != nil {
			add("runtimes.node_default: %s", err)
		}
	}
	if c.Runtimes.PythonDefault != "" {
		if err := validate.PythonVersion(c.Runtimes.PythonDefault); err != nil {
			add("runtimes.python_default: %s", err)
		}
	}
	if !strings.HasPrefix(c.Runtimes.NodeMirror, "https://") {
		add("runtimes.node_mirror must be an https URL")
	}
	switch c.Runtimes.NodeProcessManager {
	case "pm2", "direct":
	default:
		add("runtimes.node_process_manager must be pm2 or direct, got %q", c.Runtimes.NodeProcessManager)
	}

	if _, err := logLevel(c.Logging.Level); err != nil {
		add("logging.level: %s", err)
	}
	switch c.Logging.Color {
	case "auto", "always", "never":
	default:
		add("logging.color must be auto, always or never, got %q", c.Logging.Color)
	}

	if c.Users.NginxUser == "" {
		add("users.nginx_user is empty")
	}
	if c.Users.LogGroup == "" {
		add("users.log_group is empty")
	}

	if len(problems) > 0 {
		return rlerr.Usagef("%s has %d problem(s):\n  - %s",
			displayPath(c.SourcePath), len(problems), strings.Join(problems, "\n  - ")).
			WithHint("fix the file, or move it aside and run 'ratline init' to write a fresh one")
	}
	return nil
}

// HomeDir is where a user's files live.
func (c *Config) HomeDir(user string) string { return filepath.Join(c.Paths.HomeBase, user) }

// SiteDir is a site's root inside its owner's home.
func (c *Config) SiteDir(user, domain string) string {
	return filepath.Join(c.HomeDir(user), domain)
}

// RuntimeDir is the per-site directory systemd creates under /run.
func (c *Config) RuntimeDir(user, domain string) string {
	return filepath.Join(c.Paths.RunDir, validate.Slug(user, domain))
}

// SocketPath is the Unix socket nginx proxies to.
func (c *Config) SocketPath(user, domain string) string {
	return filepath.Join(c.RuntimeDir(user, domain), "app.sock")
}

// VhostPath is a site's nginx configuration file.
func (c *Config) VhostPath(domain string) string {
	return filepath.Join(c.Paths.NginxSitesAvailable, domain+".conf")
}

// VhostLink is the sites-enabled symlink for a site.
func (c *Config) VhostLink(domain string) string {
	return filepath.Join(c.Paths.NginxSitesEnabled, domain+".conf")
}

// UnitPath is a site's systemd unit file.
func (c *Config) UnitPath(user, domain string) string {
	return filepath.Join(c.Paths.SystemdDir, validate.UnitName(user, domain))
}

// HomeFileMode is users.home_mode as a mode.
func (c *Config) HomeFileMode() uint32 {
	m, _ := parseOctal(c.Users.HomeMode)
	return m
}

// SiteFileMode is users.site_mode as a mode.
func (c *Config) SiteFileMode() uint32 {
	m, _ := parseOctal(c.Users.SiteMode)
	return m
}

// UmaskValue is defaults.umask as a number.
func (c *Config) UmaskValue() int {
	m, _ := parseOctal(c.Defaults.Umask)
	return int(m)
}

func parseOctal(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty mode")
	}
	var v uint32
	for _, r := range s {
		if r < '0' || r > '7' {
			return 0, fmt.Errorf("%q is not octal", s)
		}
		v = v*8 + uint32(r-'0')
	}
	if v > 0o7777 {
		return 0, fmt.Errorf("%q is out of range", s)
	}
	return v, nil
}

func displayPath(p string) string {
	if p == "" {
		return "the built-in configuration"
	}
	return p
}
