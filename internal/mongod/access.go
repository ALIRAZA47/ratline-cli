package mongod

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

// The access list. Reachability of the MongoDB port is two facts that must agree: what
// mongod binds (all interfaces or localhost only) and what the firewall admits. `db
// access` owns both together — an operator who edits one by hand gets a server that is
// either unreachable for no visible reason or reachable by everyone, and both look fine
// from the machine itself.
//
// ufw is required, active, with a default-deny inbound policy, before the port is
// opened. That ordering is the whole safety argument: mongod starts listening on every
// interface only after it is proven that nobody but the allowed addresses can reach it.

// AccessStatus is the current shape of remote access, assembled for `db access list`
// and for doctor.
type AccessStatus struct {
	ConfManaged bool                 `json:"conf_managed"`
	BindRemote  bool                 `json:"bind_remote"`
	UfwPresent  bool                 `json:"ufw_present"`
	UfwActive   bool                 `json:"ufw_active"`
	DefaultDeny bool                 `json:"default_deny_incoming"`
	Addresses   []*state.MongoAccess `json:"addresses"`
}

// AllowResult reports what one allow changed.
type AllowResult struct {
	Address       string `json:"address"`
	AlreadyThere  bool   `json:"already_allowed"`
	OpenedNetwork bool   `json:"opened_network"` // first address: mongod now listens beyond localhost
}

// RevokeResult reports what one revoke changed.
type RevokeResult struct {
	Address       string `json:"address"`
	WasAbsent     bool   `json:"was_absent"`
	ClosedNetwork bool   `json:"closed_network"` // last address gone: back to localhost only
}

// CanonicalAddress turns what the operator typed into the canonical CIDR everything
// else stores and matches on. One address per command: the ufw rule, the state row and
// the revoke that undoes them are all singular, and a comma list would make "which of
// these failed" a real question.
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

// AccessAllow admits an address to the MongoDB port: a ufw rule, a state row, and — on
// the first address — reconfiguring mongod to listen beyond localhost.
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

	existing, err := m.State.ListMongoAccess(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range existing {
		if a.Address == canonical {
			return &AllowResult{Address: canonical, AlreadyThere: true}, nil
		}
	}

	opening := len(existing) == 0

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	if err = m.ufwAllow(ctx, rb, canonical); err != nil {
		return nil, err
	}

	row := &state.MongoAccess{Address: canonical, Note: note, CreatedBy: invoker, CreatedAt: time.Now().UTC()}
	if err = m.State.PutMongoAccess(ctx, row); err != nil {
		return nil, err
	}
	rb.Push("forget the allowed address "+canonical, func(ctx context.Context) error {
		_, derr := m.State.DeleteMongoAccess(ctx, canonical)
		return derr
	})

	if opening {
		if _, err = m.writeConf(ctx, rb, true, false); err != nil {
			return nil, err
		}
		// Verified against the local server's plain URI, not the attached one. They
		// are usually the same server, but nothing forces that — an operator can
		// re-attach ratline to a managed cluster while this mongod keeps serving —
		// and a ping to whatever is attached would then "verify" a restart it never
		// touched. The plain local ping needs no credentials and proves both facts
		// that matter here: the server answers, and it still enforces authorization.
		if _, err = m.restartAndVerify(ctx, PlainLocalURI); err != nil {
			return nil, err
		}
		// The whole command exists to change who can reach the port, so the proof is
		// the socket, not the file.
		if err = m.verifyBind(ctx, true); err != nil {
			return nil, err
		}
	}
	return &AllowResult{Address: canonical, OpenedNetwork: opening}, nil
}

// AccessRevoke removes an address, and — when it was the last one — puts mongod back
// on localhost only. Revoking an address that was never allowed is the desired state
// already, reported rather than failed.
func (m *Manager) AccessRevoke(ctx context.Context, address string) (result *RevokeResult, err error) {
	canonical, err := CanonicalAddress(address)
	if err != nil {
		return nil, err
	}
	row, gerr := m.State.GetMongoAccess(ctx, canonical)
	if gerr != nil {
		if errors.Is(gerr, state.ErrNotFound) {
			return &RevokeResult{Address: canonical, WasAbsent: true}, nil
		}
		return nil, gerr
	}
	if err := m.requireManagedConf(); err != nil {
		return nil, err
	}

	remaining, err := m.State.ListMongoAccess(ctx)
	if err != nil {
		return nil, err
	}
	closing := len(remaining) == 1

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	if err = m.ufwDelete(ctx, rb, canonical); err != nil {
		return nil, err
	}
	if _, err = m.State.DeleteMongoAccess(ctx, canonical); err != nil {
		return nil, err
	}
	rb.Push("re-record the allowed address "+canonical, func(ctx context.Context) error {
		return m.State.PutMongoAccess(ctx, row)
	})

	if closing {
		if _, err = m.writeConf(ctx, rb, false, false); err != nil {
			return nil, err
		}
		// Plain local URI for the same reason as the allow path: this command's
		// promises are about the local server, whatever happens to be attached.
		if _, err = m.restartAndVerify(ctx, PlainLocalURI); err != nil {
			return nil, err
		}
		// Closing the network is the promise this command makes; a server still
		// listening on other interfaces after "the last address was revoked" is the
		// failure that matters most.
		if err = m.verifyBind(ctx, false); err != nil {
			return nil, err
		}
	}
	return &RevokeResult{Address: canonical, ClosedNetwork: closing}, nil
}

// AccessList assembles the current state of remote access.
func (m *Manager) AccessList(ctx context.Context) (*AccessStatus, error) {
	rows, err := m.State.ListMongoAccess(ctx)
	if err != nil {
		return nil, err
	}
	conf := m.confState()
	s := &AccessStatus{
		ConfManaged: conf.Managed,
		BindRemote:  conf.Remote,
		Addresses:   rows,
	}
	s.UfwPresent = m.Bins != nil && m.Bins.Available("ufw")
	if s.UfwPresent {
		if active, deny, uerr := m.ufwStatus(ctx); uerr == nil {
			s.UfwActive, s.DefaultDeny = active, deny
		}
	}
	return s, nil
}

// Exposure is the safety question doctor asks: is this host's mongod reachable beyond
// localhost, and if so, is a firewall actually standing between it and the internet?
// Answered from the socket, not the config, for the same reason the flows verify that
// way — only the running process decides who can connect.
type Exposure struct {
	Present bool // a mongod is installed on this host
	Remote  bool // it listens beyond localhost
	Guarded bool // ufw is active with a default-deny incoming policy
	Allowed int  // how many addresses ratline has allowed through
}

// CheckExposure assembles the exposure picture for doctor. One implementation, called
// by both the subject walk and the bare sweep — a lesson written down in this
// repository twice already.
func (m *Manager) CheckExposure(ctx context.Context) (*Exposure, error) {
	e := &Exposure{Present: m.Installed()}
	if !e.Present {
		return e, nil
	}
	remote, err := m.ListensRemotely(ctx)
	if err != nil {
		// Without ss the socket cannot be asked; the managed config is the next best
		// witness, and for an unmanaged mongod the answer stays "not known to be
		// exposed" rather than a guess.
		remote = m.confState().Remote
	}
	e.Remote = remote
	if m.Bins != nil && m.Bins.Available("ufw") {
		if active, deny, uerr := m.ufwStatus(ctx); uerr == nil {
			e.Guarded = active && deny
		}
	}
	if m.State != nil {
		if rows, rerr := m.State.ListMongoAccess(ctx); rerr == nil {
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
		return rlerr.Preconditionf("there is no MongoDB server on this host for ratline to open or close").
			WithHint("'ratline db install' sets one up. For a server elsewhere — Atlas, another " +
				"host — the access list lives with that server, not with this machine's firewall")
	}
	return rlerr.Preconditionf("the MongoDB server on this host is not managed by ratline").
		WithHint("'ratline db access' rewrites %s to move mongod between localhost-only and "+
			"all-interfaces, and it will not rewrite a config it did not create. Manage the "+
			"bind address and firewall yourself, or set the server up with 'ratline db install'", ConfPath)
}

// requireGuardingFirewall proves the firewall would actually stand between the port
// and the internet before mongod is told to listen. Three separate refusals rather
// than one, because each has a different fix.
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
			WithHint("allow SSH first — 'ufw allow OpenSSH' — then 'ufw enable'. ratline " +
				"never enables the firewall itself: done in the wrong order it locks you " +
				"out of this machine, and only you know what else has to stay reachable")
	}
	if !deny {
		return rlerr.Preconditionf("ufw's default incoming policy is allow, so an allow-list of addresses would restrict nobody").
			WithHint("ufw default deny incoming — after checking 'ufw status numbered' " +
				"covers everything this server must keep serving")
	}
	return nil
}

// ufwStatus reports whether ufw is active and whether its default incoming policy
// denies. Parsed from `ufw status verbose`, which is stable in exactly the two lines
// read here.
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

// ufwRuleSpec is the rule an allow creates and a revoke deletes, one argv, no
// formatting beyond the address itself — which CanonicalAddress has already reduced to
// a CIDR.
func ufwRuleSpec(address string) []string {
	return []string{"allow", "proto", "tcp", "from", address, "to", "any", "port", Port}
}

func (m *Manager) ufwAllow(ctx context.Context, rb *system.Rollback, address string) error {
	args := append(ufwRuleSpec(address), "comment", "managed-by: ratline (db access allow)")
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
	// A rule that is already gone is the outcome asked for. ufw phrases it as a
	// complaint; some versions also exit non-zero about it.
	if err != nil && (res == nil || !strings.Contains(res.Stdout+res.Stderr, "non-existent")) {
		return err
	}
	rb.Push("re-add the ufw rule for "+address, func(ctx context.Context) error {
		args := append(ufwRuleSpec(address), "comment", "managed-by: ratline (db access allow)")
		_, aerr := m.Runner.Run(ctx, system.Cmd{
			Name: "ufw", Args: args, Mutates: true, Label: "ufw allow " + address,
		})
		return aerr
	})
	return nil
}
