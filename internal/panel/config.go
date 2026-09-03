// Package panel wires the web panel together: its configuration, its HTTP server,
// its database and the ratline client every request ultimately goes through.
package panel

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// DefaultConfigPath is where the panel looks unless --config says otherwise.
//
// Beside ratline's own configuration rather than in a directory of its own: an
// operator looking for "the ratline configuration" should find both files in one
// place, and the panel is not a separate product to administer.
const DefaultConfigPath = "/etc/ratline/panel.yaml"

// EnvConfigPath is the environment override, matching ratline's own.
const EnvConfigPath = "RATLINE_PANEL_CONFIG"

// SchemaVersion is bumped when a change needs a migration rather than a merge.
const SchemaVersion = 1

// Duration reads as "30s" in YAML, the same as ratline's.
type Duration time.Duration

func (d Duration) D() time.Duration          { return time.Duration(d) }
func (d Duration) String() string            { return time.Duration(d).String() }
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return rlerr.Usagef("line %d: a duration must be a string such as \"30s\"", n.Line)
	}
	v, err := validate.Duration(s)
	if err != nil {
		return rlerr.Usagef("line %d: %s", n.Line, err.Error())
	}
	*d = Duration(v)
	return nil
}

// Config is /etc/ratline/panel.yaml.
type Config struct {
	Version  int      `yaml:"version"`
	Listen   Listen   `yaml:"listen"`
	Ratline  Ratline  `yaml:"ratline"`
	Paths    Paths    `yaml:"paths"`
	Session  Session  `yaml:"session"`
	Security Security `yaml:"security"`
	Jobs     Jobs     `yaml:"jobs"`
	Logging  Logging  `yaml:"logging"`

	// SourcePath and Loaded describe where this came from, for `doctor`.
	SourcePath string `yaml:"-"`
	Loaded     bool   `yaml:"-"`
}

// Listen is where the panel answers.
type Listen struct {
	// Address defaults to 127.0.0.1 and should usually stay there.
	//
	// The panel is a root-equivalent surface: anybody who can sign in can
	// provision services on this machine. Binding it to the loopback and putting
	// nginx in front means the thing exposed to the internet is nginx, with a
	// certificate, a real TLS configuration and logs — rather than a Go server
	// somebody has to remember to keep patched.
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
	// Domain is the public name, once `ratline-panel domain set` has been run.
	// The panel needs to know it to decide whether a cookie may be marked Secure
	// and to check the Origin of a state-changing request.
	Domain string `yaml:"domain"`
	// TrustProxy makes the panel believe X-Forwarded-For and X-Forwarded-Proto.
	// True only because nginx on the same host sets them; a panel reachable
	// directly must not, or every client can claim any address it likes and the
	// rate limiter counts them separately.
	TrustProxy bool `yaml:"trust_proxy"`
}

// Ratline is how the panel reaches the CLI.
type Ratline struct {
	Binary string `yaml:"binary"`
	// Config passes --config through. Empty means ratline finds its own, which is
	// what a normal install wants.
	Config string `yaml:"config"`
	// Timeouts, so an operator with a slow build can raise the job ceiling without
	// recompiling anything.
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	JobTimeout   Duration `yaml:"job_timeout"`
}

// Paths is what the panel owns on disk.
type Paths struct {
	StateDB  string `yaml:"state_db"`
	AuditLog string `yaml:"audit_log"`
	// Nginx is the vhost `domain set` writes. Under nginx's own directory, with
	// a managed-by header, and never touched if the header is missing.
	NginxVhost string `yaml:"nginx_vhost"`
}

// Session governs how long a browser stays signed in.
type Session struct {
	// TTL is the absolute lifetime. A session ends at TTL after it began however
	// active it has been, so a stolen cookie has a horizon.
	TTL Duration `yaml:"ttl"`
	// IdleTimeout ends a session that has gone quiet, which is the case that
	// matters on a shared laptop.
	IdleTimeout Duration `yaml:"idle_timeout"`
	// SecureCookie: auto, always or never. auto marks the cookie Secure when the
	// request arrived over HTTPS, which is what makes the panel usable on
	// http://localhost during setup without shipping a cookie that leaks in the
	// clear once it is on a domain.
	SecureCookie string `yaml:"secure_cookie"`
	// CookieName, so two panels behind one domain do not overwrite each other.
	CookieName string `yaml:"cookie_name"`
}

// Security is the rest of the front door.
type Security struct {
	// RequireTOTP refuses to let an account do anything until it has enrolled a
	// second factor. Off by default because a panel nobody can sign in to is not
	// secure, it is broken; on is the right answer for a panel on the internet.
	RequireTOTP bool `yaml:"require_totp"`
	// InviteTTL bounds how long an invitation link works.
	InviteTTL Duration `yaml:"invite_ttl"`
	// MaxFailedLogins and LoginWindow are the rate limit, counted per account and
	// per source address independently.
	MaxFailedLogins int      `yaml:"max_failed_logins"`
	LoginWindow     Duration `yaml:"login_window"`
	// AllowFrom, when set, refuses every request from outside these CIDRs. A
	// second lock on the door for a panel that is reachable from the internet and
	// only ever used from an office.
	AllowFrom []string `yaml:"allow_from"`
}

// Jobs bounds the history.
type Jobs struct {
	Retain      int `yaml:"retain"`
	OutputLimit int `yaml:"output_limit_bytes"`
	// Concurrency is one, and the comment is the reason: ratline takes a global
	// lock for every mutation, so a second job would sit waiting for the first and
	// report "locked" if it timed out. Serialising here turns that into a queue
	// somebody can watch instead of a failure they have to retry.
	Concurrency int `yaml:"concurrency"`
}

// Logging mirrors ratline's.
type Logging struct {
	Level string `yaml:"level"`
	JSON  bool   `yaml:"json"`
}

// Default returns the configuration a fresh install runs with.
func Default() *Config {
	return &Config{
		Version: SchemaVersion,
		Listen: Listen{
			Address:    "127.0.0.1",
			Port:       8420,
			TrustProxy: true,
		},
		Ratline: Ratline{
			Binary:       "/usr/local/bin/ratline",
			ReadTimeout:  Duration(45 * time.Second),
			WriteTimeout: Duration(5 * time.Minute),
			JobTimeout:   Duration(45 * time.Minute),
		},
		Paths: Paths{
			StateDB:    "/var/lib/ratline/panel.db",
			AuditLog:   "/var/log/ratline/panel-audit.log",
			NginxVhost: "/etc/nginx/sites-available/ratline-panel.conf",
		},
		Session: Session{
			TTL:          Duration(12 * time.Hour),
			IdleTimeout:  Duration(2 * time.Hour),
			SecureCookie: "auto",
			CookieName:   "ratline_panel",
		},
		Security: Security{
			InviteTTL:       Duration(72 * time.Hour),
			MaxFailedLogins: 8,
			LoginWindow:     Duration(15 * time.Minute),
		},
		Jobs: Jobs{
			Retain:      300,
			OutputLimit: 1 << 20,
			Concurrency: 1,
		},
		Logging: Logging{Level: "info"},
	}
}

// LoadOrDefault reads the file, falling back to the defaults when it is absent.
//
// Absent is not an error: the panel has to start before `ratline-panel install` has
// written anything, or the installer would have to hand-write a file to run the tool
// that writes files.
func LoadOrDefault(path string) (*Config, error) {
	cfg := Default()
	cfg.SourcePath = path
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own configuration
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, cfg.Validate()
		}
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s", path)
	}
	// Unmarshalling onto the defaults means an absent key keeps its default rather
	// than becoming a zero — the same merge ratline's own loader does, and the
	// reason a half-written file does not silently set every timeout to zero.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeUsage, "reading %s", path).
			WithHint("run 'ratline-panel config validate' after editing it")
	}
	cfg.Loaded = true
	cfg.SourcePath = path
	return cfg, cfg.Validate()
}

// ConfigPath resolves the flag, the environment and the default in that order.
func ConfigPath(flag string) string {
	if flag != "" {
		return flag
	}
	if p := strings.TrimSpace(os.Getenv(EnvConfigPath)); p != "" {
		return p
	}
	return DefaultConfigPath
}

// Validate refuses a configuration that would produce a server nobody meant.
func (c *Config) Validate() error {
	if c.Version > SchemaVersion {
		return rlerr.Preconditionf("%s declares version %d; this ratline-panel understands %d",
			c.SourcePath, c.Version, SchemaVersion).
			WithHint("upgrade ratline-panel rather than downgrading the file")
	}
	if err := validate.Port(c.Listen.Port); err != nil {
		return err
	}
	if c.Listen.Address == "" {
		return rlerr.Usagef("listen.address is empty").
			WithHint("127.0.0.1 to serve behind nginx, 0.0.0.0 to expose the port directly")
	}
	if c.Listen.Domain != "" {
		d, err := validate.Domain(c.Listen.Domain)
		if err != nil {
			return err
		}
		c.Listen.Domain = d
	}
	for _, p := range []struct{ name, value string }{
		{"ratline.binary", c.Ratline.Binary},
		{"paths.state_db", c.Paths.StateDB},
		{"paths.audit_log", c.Paths.AuditLog},
		{"paths.nginx_vhost", c.Paths.NginxVhost},
	} {
		if p.value == "" {
			return rlerr.Usagef("%s is empty", p.name)
		}
		if _, err := validate.AbsClean(p.value); err != nil {
			return rlerr.Usagef("%s: %s", p.name, err.Error())
		}
	}
	if c.Ratline.Config != "" {
		if _, err := validate.AbsClean(c.Ratline.Config); err != nil {
			return rlerr.Usagef("ratline.config: %s", err.Error())
		}
	}
	switch c.Session.SecureCookie {
	case "auto", "always", "never":
	default:
		return rlerr.Usagef("session.secure_cookie must be auto, always or never").
			WithHint("auto marks the cookie Secure when the request arrived over HTTPS")
	}
	if c.Session.TTL.D() <= 0 || c.Session.IdleTimeout.D() <= 0 {
		return rlerr.Usagef("session.ttl and session.idle_timeout must both be positive")
	}
	if c.Session.IdleTimeout.D() > c.Session.TTL.D() {
		return rlerr.Usagef("session.idle_timeout is longer than session.ttl, so it can never apply").
			WithHint("the idle timeout ends a quiet session early; it cannot extend one")
	}
	if c.Session.CookieName == "" {
		return rlerr.Usagef("session.cookie_name is empty")
	}
	if c.Security.MaxFailedLogins < 1 {
		return rlerr.Usagef("security.max_failed_logins must be at least 1").
			WithHint("0 would lock every account out on the first typo")
	}
	if _, err := parseCIDRs(c.Security.AllowFrom); err != nil {
		return err
	}
	if c.Jobs.Concurrency < 1 {
		return rlerr.Usagef("jobs.concurrency must be at least 1")
	}
	if c.Jobs.OutputLimit < 4096 {
		return rlerr.Usagef("jobs.output_limit_bytes must be at least 4096").
			WithHint("a transcript shorter than that cannot hold a useful failure")
	}
	return nil
}

// PublicURL is the address to hand somebody, for an invitation link.
func (c *Config) PublicURL() string {
	if c.Listen.Domain != "" {
		return "https://" + c.Listen.Domain
	}
	host := c.Listen.Address
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(c.Listen.Port))
}

// Write renders the configuration to disk, atomically and 0640 root:root.
func (c *Config) Write(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the panel configuration")
	}
	body := append([]byte(configHeader), data...)
	if _, err := system.EnsureDir(filepath.Dir(path), 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	return system.WriteFileAtomic(path, body, 0o640, 0, 0)
}

const configHeader = `# ratline-panel configuration.
#
# The panel is a caller of the ratline binary, not a second copy of it: everything it
# does, it does by running 'ratline ... --json' and reading the envelope. What is
# configured here is the front door — where it listens, how long a session lasts, and
# where its own database lives.
#
# Regenerate the commented reference with:
#   ratline-panel config reference

`
