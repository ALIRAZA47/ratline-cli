// Package mysqld installs and configures the MySQL/MariaDB server on this host.
//
// The split with internal/mysql is the same as MongoDB's: that package manages what lives
// inside a server it is pointed at (databases, users, dumps); this one manages the server
// itself — the distro package, the managed bind-address drop-in, the systemd unit, and the
// firewall in front of the port. Nothing here runs unless an operator asked for it with
// `ratline db install --engine mysql` or is steering reachability with `db access`.
//
// Distro-native by choice: mysql-server on Ubuntu, mariadb-server on Debian. There is no
// third-party apt repository or signing key to manage — the package comes from the
// distribution — so this is simpler than the MongoDB installer. What it keeps is the
// discipline: every change is staged and verified against the running server, not asserted
// from the config file.
package mysqld

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Engine is the state/access key for MySQL rows.
const Engine = "mysql"

// Port is deliberately not configurable: every generated connection string, ufw rule and
// doctor check agrees on it by construction.
const Port = "3306"

// Manager installs and configures the MySQL server on this host.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	OS     system.OSInfo
	DryRun bool

	// Test seams, all zero/nil in production.
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

// abs maps a fixed system path through the test seam.
func (m *Manager) abs(path string) string {
	if m.FSRoot == "" {
		return path
	}
	return filepath.Join(m.FSRoot, path)
}

// db returns the inside-the-server manager, for the client operations install shares with
// the rest of the db commands.
func (m *Manager) db() *mysql.Manager {
	return &mysql.Manager{
		Cfg: m.Cfg, Log: m.Log, Runner: m.Runner, Bins: m.Bins, State: m.State, DryRun: m.DryRun,
	}
}

// Installed reports whether the MySQL server binary is on this host.
func (m *Manager) Installed() bool {
	if m.InstalledProbe != nil {
		return m.InstalledProbe()
	}
	return m.Bins != nil && m.Bins.Available("mysqld")
}

// ConfState is what the managed bind drop-in on this host is, as far as ratline knows.
type ConfState struct {
	Exists  bool
	Managed bool // carries the managed-by header
	Remote  bool // bind-address opens beyond localhost
}

// confState inspects the managed drop-in without parsing a full my.cnf: the file is either
// ratline's own render, whose text is known, or absent.
func (m *Manager) confState() ConfState {
	body, err := os.ReadFile(m.confPath())
	if err != nil {
		return ConfState{}
	}
	s := ConfState{Exists: true}
	text := string(body)
	s.Managed = strings.HasPrefix(text, system.ManagedHeader) ||
		strings.HasPrefix(text, "# "+system.ManagedHeader)
	s.Remote = strings.Contains(text, "bind-address") && strings.Contains(text, "0.0.0.0")
	return s
}

// confPath is the managed bind drop-in path, chosen per distro so it loads after the
// package's own server config and its bind-address wins.
func (m *Manager) confPath() string { return m.abs(m.distro().ConfDropIn) }
