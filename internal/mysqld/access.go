package mysqld

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The access list, the same shape as MongoDB's: reachability is what the server binds and
// what the firewall admits, and these two facts must agree. `db access` owns both, in the
// only safe order — the firewall rule before the wider bind — and refuses unless ufw is
// active with a default-deny incoming policy. ratline never enables ufw itself.

// AccessStatus is the current shape of remote access, for `db access list` and doctor.
type AccessStatus struct {
	ConfManaged bool                  `json:"conf_managed"`
	BindRemote  bool                  `json:"bind_remote"`
	UfwPresent  bool                  `json:"ufw_present"`
	UfwActive   bool                  `json:"ufw_active"`
	DefaultDeny bool                  `json:"default_deny_incoming"`
	Addresses   []*state.EngineAccess `json:"addresses"`
}

// AllowResult reports what one allow changed.
type AllowResult struct {
	Address       string `json:"address"`
	AlreadyThere  bool   `json:"already_allowed"`
	OpenedNetwork bool   `json:"opened_network"`
}

// RevokeResult reports what one revoke changed.
type RevokeResult struct {
	Address       string `json:"address"`
	WasAbsent     bool   `json:"was_absent"`
	ClosedNetwork bool   `json:"closed_network"`
}

// Exposure is the doctor-facing summary.
type Exposure struct {
	Present bool `json:"present"`
	Remote  bool `json:"remote"`
	Guarded bool `json:"guarded"`
	Allowed int  `json:"allowed"`
}

// CanonicalAddress reduces what the operator typed to the canonical CIDR everything else
// stores and matches on. One address per command.
func CanonicalAddress(raw string) (string, error) {
	list, err := validate.CIDRList(raw)
	if err != nil {
		return "", err
	}
	if len(list) != 1 {
		return "", rlerr.Usagef("one address per command").
			WithHint("run 'ratline db access allow' once per address or network")
	}
	return list[0], nil
}

// AccessAllow admits an address to the MySQL port: a ufw rule, a state row, and — on the
// first address — reconfiguring the server to listen beyond localhost.
func (m *Manager) AccessAllow(ctx context.Context, address, note, invoker string) (result *AllowResult, err error) {
	canonical, err := CanonicalAddress(address)
	if err != nil {
		return nil, err
	}
	if err := m.requireManagedConf(); err != nil {
		return nil, err
	}
	if err := m.requireGuardingFirewall(ctx); err != nil {
		return nil, err
	}

	existing, err := m.State.ListEngineAccess(ctx, Engine)
	if err != nil {
		return nil, err
	}
	for _, a := range existing {
		if a.Address == canonical {
			return &AllowResult{Address: canonical, AlreadyThere: true}, nil
		}
	}
	opening := len(existing) == 0

	// The bind widens by restart, verified with the admin credentials — resolved before
	// anything changes, so a missing attachment refuses here rather than halfway.
	var adminDefaults string
	if opening {
		if adminDefaults, err = m.db().AdminDefaultsFile(); err != nil {
			return nil, err
		}
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	if err = m.ufwAllow(ctx, rb, canonical); err != nil {
		return nil, err
	}
	row := &state.EngineAccess{Engine: Engine, Address: canonical, Note: note, CreatedBy: invoker, CreatedAt: time.Now().UTC()}
	if err = m.State.PutEngineAccess(ctx, row); err != nil {
		return nil, err
	}
	rb.Push("forget the allowed address "+canonical, func(ctx context.Context) error {
		_, derr := m.State.DeleteEngineAccess(ctx, Engine, canonical)
		return derr
	})

	if opening {
		if _, err = m.writeConf(ctx, rb, true, false); err != nil {
			return nil, err
		}
		if _, err = m.restartAndVerify(ctx, adminDefaults); err != nil {
			return nil, err
		}
		if err = m.verifyBind(ctx, true); err != nil {
			return nil, err
		}
	}
	return &AllowResult{Address: canonical, OpenedNetwork: opening}, nil
}

// AccessRevoke removes an address, and — when it was the last — puts the server back on
// localhost only.
func (m *Manager) AccessRevoke(ctx context.Context, address string) (result *RevokeResult, err error) {
	canonical, err := CanonicalAddress(address)
	if err != nil {
		return nil, err
	}
	row, gerr := m.State.GetEngineAccess(ctx, Engine, canonical)
	if gerr != nil {
		if errors.Is(gerr, state.ErrNotFound) {
			return &RevokeResult{Address: canonical, WasAbsent: true}, nil
		}
		return nil, gerr
	}
	if err := m.requireManagedConf(); err != nil {
		return nil, err
	}

	remaining, err := m.State.ListEngineAccess(ctx, Engine)
	if err != nil {
		return nil, err
	}
	closing := len(remaining) == 1
	var adminDefaults string
	if closing {
		if adminDefaults, err = m.db().AdminDefaultsFile(); err != nil {
			return nil, err
		}
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	if err = m.ufwDelete(ctx, rb, canonical); err != nil {
		return nil, err
	}
	if _, err = m.State.DeleteEngineAccess(ctx, Engine, canonical); err != nil {
		return nil, err
	}
	rb.Push("re-record the allowed address "+canonical, func(ctx context.Context) error {
		return m.State.PutEngineAccess(ctx, row)
	})

	if closing {
		if _, err = m.writeConf(ctx, rb, false, false); err != nil {
			return nil, err
		}
		if _, err = m.restartAndVerify(ctx, adminDefaults); err != nil {
			return nil, err
		}
		if err = m.verifyBind(ctx, false); err != nil {
			return nil, err
		}
	}
	return &RevokeResult{Address: canonical, ClosedNetwork: closing}, nil
}

// AccessList assembles the current state of remote access.
func (m *Manager) AccessList(ctx context.Context) (*AccessStatus, error) {
	rows, err := m.State.ListEngineAccess(ctx, Engine)
	if err != nil {
		return nil, err
	}
	conf := m.confState()
	s := &AccessStatus{ConfManaged: conf.Managed, BindRemote: conf.Remote, Addresses: rows}
	s.UfwPresent = m.Bins != nil && m.Bins.Available("ufw")
	if s.UfwPresent {
		if active, deny, uerr := m.ufwStatus(ctx); uerr == nil {
			s.UfwActive, s.DefaultDeny = active, deny
		}
	}
	return s, nil
}

// CheckExposure assembles the exposure picture for doctor: is the server reachable beyond
// localhost with no firewall standing guard? Answered from the socket and ufw, not the
// config.
func (m *Manager) CheckExposure(ctx context.Context) (*Exposure, error) {
	e := &Exposure{Present: m.Installed()}
	if !e.Present {
		return e, nil
	}
	remote, err := m.ListensRemotely(ctx)
	if err != nil {
		remote = m.confState().Remote
	}
	e.Remote = remote
	if m.Bins != nil && m.Bins.Available("ufw") {
		if active, deny, uerr := m.ufwStatus(ctx); uerr == nil {
			e.Guarded = active && deny
		}
	}
	if m.State != nil {
		if rows, rerr := m.State.ListEngineAccess(ctx, Engine); rerr == nil {
			e.Allowed = len(rows)
		}
	}
	return e, nil
}

func (m *Manager) requireManagedConf() error {
	conf := m.confState()
	if conf.Managed {
		return nil
	}
	if !conf.Exists && !m.Installed() {
		return rlerr.Preconditionf("there is no MySQL server on this host for ratline to open or close").
			WithHint("'ratline db install --engine mysql' sets one up. For a server elsewhere, the " +
				"access list lives with that server, not with this machine's firewall")
	}
	return rlerr.Preconditionf("the MySQL server on this host is not managed by ratline").
		WithHint("'ratline db access' rewrites %s, and will not rewrite a config it did not create",
			m.distro().ConfDropIn)
}

// requireGuardingFirewall proves the firewall stands between the port and the internet
// before the server is told to listen. Three refusals, each with its own fix.
func (m *Manager) requireGuardingFirewall(ctx context.Context) error {
	if m.Bins == nil || !m.Bins.Available("ufw") {
		return rlerr.Preconditionf("ufw is not installed, and without a firewall an allowed-addresses list is fiction").
			WithHint("apt-get install ufw, allow SSH first (ufw allow OpenSSH), then ufw enable")
	}
	active, deny, err := m.ufwStatus(ctx)
	if err != nil {
		return err
	}
	if !active {
		return rlerr.Preconditionf("ufw is installed but not active, so nothing would stop the addresses you did not allow").
			WithHint("allow SSH first — 'ufw allow OpenSSH' — then 'ufw enable'. ratline never " +
				"enables the firewall itself: done in the wrong order it locks you out of SSH")
	}
	if !deny {
		return rlerr.Preconditionf("ufw's default incoming policy is allow, so an allow-list would restrict nobody").
			WithHint("ufw default deny incoming — after checking 'ufw status numbered' covers " +
				"everything this server must keep serving")
	}
	return nil
}

func (m *Manager) ufwStatus(ctx context.Context) (active, defaultDeny bool, err error) {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "ufw", Args: []string{"status", "verbose"}, Label: "ufw status",
	})
	if err != nil {
		return false, false, rlerr.Wrap(err, rlerr.CodeExternal, "asking ufw for its status")
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Status:"); ok {
			active = strings.TrimSpace(v) == "active"
		}
		if v, ok := strings.CutPrefix(line, "Default:"); ok {
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if strings.HasSuffix(part, "(incoming)") {
					word := strings.TrimSpace(strings.TrimSuffix(part, "(incoming)"))
					defaultDeny = word == "deny" || word == "reject"
				}
			}
		}
	}
	return active, defaultDeny, nil
}

func ufwRuleSpec(address string) []string {
	return []string{"allow", "proto", "tcp", "from", address, "to", "any", "port", Port}
}

func (m *Manager) ufwAllow(ctx context.Context, rb *system.Rollback, address string) error {
	args := append(ufwRuleSpec(address), "comment", "managed-by: ratline (db access allow, mysql)")
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "ufw", Args: args, Mutates: true, Label: "ufw allow " + address,
	}); err != nil {
		return err
	}
	rb.Push("delete the ufw rule for "+address, func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "ufw", Args: append([]string{"delete"}, ufwRuleSpec(address)...),
			Mutates: true, Label: "ufw delete " + address,
		})
		return err
	})
	return nil
}

func (m *Manager) ufwDelete(ctx context.Context, rb *system.Rollback, address string) error {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "ufw", Args: append([]string{"delete"}, ufwRuleSpec(address)...),
		Mutates: true, Label: "ufw delete " + address,
	})
	if err != nil && (res == nil || !strings.Contains(res.Stdout+res.Stderr, "non-existent")) {
		return err
	}
	rb.Push("re-add the ufw rule for "+address, func(ctx context.Context) error {
		args := append(ufwRuleSpec(address), "comment", "managed-by: ratline (db access allow, mysql)")
		_, aerr := m.Runner.Run(ctx, system.Cmd{
			Name: "ufw", Args: args, Mutates: true, Label: "ufw allow " + address,
		})
		return aerr
	})
	return nil
}
