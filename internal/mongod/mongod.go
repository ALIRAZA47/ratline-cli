// Package mongod installs and configures the MongoDB server on this host.
//
// The split with internal/mongo is deliberate: that package manages what lives inside
// a MongoDB server ratline is pointed at — databases, users, dumps — and works the same
// against a local mongod and Atlas. This one manages the server itself: the apt
// repository and its signing key, the package, /etc/mongod.conf, the systemd unit, and
// the firewall rules in front of the port. Nothing here runs unless an operator asked
// for the server with `ratline db install` or is steering its reachability with
// `ratline db access`.
//
// Two rules carry over from the rest of ratline and matter doubly here:
//
//   - The admin password never touches argv. It reaches mongosh through the
//     environment, exactly as every other credential does.
//   - Every change is verified by its effect, not its syntax. Authorization is proven
//     enabled by asking the running server, because a config file that says
//     "authorization: enabled" and a server that enforces it are two different things —
//     the RevokedKeys incident was a syntactically perfect config that locked a server.
package mongod

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

const (
	// ConfPath is where the mongodb-org package puts its configuration and where the
	// unit reads it from. There is no conf.d mechanism to drop into — mongod reads
	// exactly one file — so ratline takes the file over, with the managed header
	// marking whose it is.
	ConfPath = "/etc/mongod.conf"

	// ServiceName is the systemd unit the mongodb-org package ships.
	ServiceName = "mongod"

	// Port is deliberately not configurable: every generated connection string, ufw
	// rule and doctor check agrees on it by construction.
	Port = "27017"

	// PlainLocalURI reaches a mongod that does not enforce authorization yet — the
	// state a freshly installed package starts in, and the only moment ratline ever
	// connects without credentials.
	PlainLocalURI = "mongodb://127.0.0.1:27017"
)

// Manager installs and configures the MongoDB server on this host.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	OS     system.OSInfo
	DryRun bool

	// StartWait bounds how long to wait for mongod to answer after a start or
	// restart. Zero means the default; tests shrink it.
	StartWait time.Duration

	// PollInterval is the pause between connection attempts while waiting. Zero
	// means the default; tests shrink it.
	PollInterval time.Duration

	// FSRoot prefixes every absolute path this manager touches — /etc/mongod.conf,
	// the keyring, the apt sources file. Empty in production; tests point it at a
	// temporary directory so nothing real is read or written.
	FSRoot string

	// InstalledProbe overrides how "is mongod on this host" is decided. Nil means
	// the binaries registry; tests pin it so the answer never depends on what the
	// machine running the tests happens to have installed.
	InstalledProbe func() bool
}

// abs maps a fixed system path through the test seam.
func (m *Manager) abs(path string) string {
	if m.FSRoot == "" {
		return path
	}
	return filepath.Join(m.FSRoot, path)
}

func (m *Manager) confPath() string { return m.abs(ConfPath) }

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

// db returns the inside-the-server manager, for the mongosh operations install and
// access share with the rest of the db commands.
func (m *Manager) db() *mongo.Manager {
	return &mongo.Manager{
		Cfg: m.Cfg, Log: m.Log, Runner: m.Runner, Bins: m.Bins, State: m.State, DryRun: m.DryRun,
	}
}

// ConfState is what /etc/mongod.conf on this host is, as far as ratline is concerned.
type ConfState struct {
	Exists  bool
	Managed bool // carries the managed-by header, so ratline may rewrite it
	Remote  bool // listening beyond localhost (bindIpAll), per the managed template
}

// ReadConfState inspects a configuration file without parsing YAML: the file is
// either ratline's own render, whose exact text is known, or somebody else's, in which
// case the only fact needed is that it must not be touched.
func ReadConfState(path string) ConfState {
	body, err := os.ReadFile(path)
	if err != nil {
		return ConfState{}
	}
	s := ConfState{Exists: true}
	text := string(body)
	s.Managed = strings.HasPrefix(text, system.ManagedHeader)
	s.Remote = strings.Contains(text, "bindIpAll: true")
	return s
}

func (m *Manager) confState() ConfState { return ReadConfState(m.confPath()) }

// Installed reports whether the MongoDB server binary is on this host.
func (m *Manager) Installed() bool {
	if m.InstalledProbe != nil {
		return m.InstalledProbe()
	}
	return m.Bins != nil && m.Bins.Available("mongod")
}
