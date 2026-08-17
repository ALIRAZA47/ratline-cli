// Package redisd installs and configures the Redis server on this host.
//
// The split with internal/redis is the same as the other engines: that package manages
// what lives inside a server (ACL users confined to keyspaces); this one manages the
// server — the distro package, the configuration, the systemd unit, and the firewall.
//
// Redis has no conf.d directory, so ratline does not drop a file into one. It appends a
// single `include` line to the stock redis.conf — once, marked — and owns the file that
// line points at, so the distribution's own tuning is left in place and only ratline's
// directives are managed. ACL users live in an aclfile, which `ACL SAVE` persists, so a
// user created with `db create` survives a restart.
package redisd

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/redis"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

const (
	// Engine is the state/access key for Redis rows.
	Engine = "redis"
	// Port is fixed by design, like the other engines.
	Port = "6379"
	// ServiceName is the systemd unit the redis-server package ships.
	ServiceName = "redis-server"
	// Package is the distribution package; the same name on Ubuntu and Debian.
	Package = "redis-server"

	// MainConf is the distribution's own configuration, which ratline appends one
	// include to rather than taking over.
	MainConf = "/etc/redis/redis.conf"
	// ConfPath is the managed include: ratline's directives, loaded last so they win.
	ConfPath = "/etc/redis/ratline.conf"
	// ACLFile holds the ACL users, including the admin (default) user; ACL SAVE writes it.
	ACLFile = "/etc/redis/ratline-users.acl"

	includeLine = "include " + ConfPath
)

// Manager installs and configures the Redis server on this host.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	OS     system.OSInfo
	DryRun bool

	StartWait      time.Duration
	PollInterval   time.Duration
	FSRoot         string
	InstalledProbe func() bool
}

func (m *Manager) startWait() time.Duration {
	if m.StartWait > 0 {
		return m.StartWait
	}
	return 60 * time.Second
}

func (m *Manager) pollInterval() time.Duration {
	if m.PollInterval > 0 {
		return m.PollInterval
	}
	return time.Second
}

func (m *Manager) abs(path string) string {
	if m.FSRoot == "" {
		return path
	}
	return filepath.Join(m.FSRoot, path)
}

func (m *Manager) confPath() string { return m.abs(ConfPath) }
func (m *Manager) mainConf() string { return m.abs(MainConf) }
func (m *Manager) aclFile() string  { return m.abs(ACLFile) }

// db returns the inside-the-server manager.
func (m *Manager) db() *redis.Manager {
	return &redis.Manager{Cfg: m.Cfg, Log: m.Log, Runner: m.Runner, Bins: m.Bins, State: m.State, DryRun: m.DryRun}
}

// Installed reports whether the Redis server binary is on this host.
func (m *Manager) Installed() bool {
	if m.InstalledProbe != nil {
		return m.InstalledProbe()
	}
	return m.Bins != nil && m.Bins.Available("redis-server")
}

// ConfState is what the managed include on this host is.
type ConfState struct {
	Exists  bool
	Managed bool
	Remote  bool // bound beyond localhost
}

func (m *Manager) confState() ConfState {
	body, err := os.ReadFile(m.confPath())
	if err != nil {
		return ConfState{}
	}
	s := ConfState{Exists: true}
	text := string(body)
	s.Managed = strings.HasPrefix(text, system.ManagedHeader) ||
		strings.HasPrefix(text, "# "+system.ManagedHeader)
	// The managed render binds all interfaces with `bind * -::*` when remote.
	s.Remote = strings.Contains(text, "bind * ")
	return s
}
