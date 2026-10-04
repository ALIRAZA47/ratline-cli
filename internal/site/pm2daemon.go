package site

import (
	"context"
	"sort"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// syncPM2Daemons brings a tenant's shared PM2 daemons in line with their sites.
//
// Computed from the whole tenant every time, rather than patched site by site, because
// a daemon's unit is a function of every site in it — which ones it starts, the sum of
// their ceilings, the union of their relaxations — and a patch that forgot one caller
// would leave a daemon starting a disabled site or holding a deleted one. Run wherever
// a PM2 site's unit changes shape or a site comes or goes:
//
//   - changed is the site being applied, enabled or disabled, as it is about to be. It
//     replaces its state row, or joins the list when `site add` has not written one.
//   - gone is the domain of a site being deleted, left out whatever state says.
//
// A daemon no site needs any more — the last PM2 site of a tenant on that Node version
// deleted, moved to another version, or switched to direct — is stopped and removed.
// That is last, after every daemon that is staying has been written, so a failure part
// way leaves nothing running that a site still needs.
func (m *Manager) syncPM2Daemons(ctx context.Context, owner string, changed *state.Site, gone string, rb *system.Rollback) error {
	sites, err := m.State.ListSites(ctx, state.SiteFilter{Owner: owner})
	if err != nil {
		return err
	}
	if changed != nil {
		replaced := false
		for i, s := range sites {
			if s.Domain == changed.Domain {
				sites[i], replaced = changed, true
			}
		}
		if !replaced {
			sites = append(sites, changed)
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })

	var daemons map[string]*unit.PM2Daemon
	for _, s := range sites {
		if s.Domain == gone || !s.Dynamic() || (s.Runtime != "node" && s.Runtime != "bun") {
			continue
		}
		if daemons == nil {
			daemons = map[string]*unit.PM2Daemon{}
		}
		id, err := m.identity(s.Owner)
		if err != nil {
			return err
		}
		rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, s, id, m.DryRun)
		if runtime.ProcessManagerFor(rc) != runtime.ProcessManagerPM2 {
			continue
		}
		d, err := runtime.Node{}.PM2Daemon(rc)
		if err != nil {
			if changed != nil && s.Domain == changed.Domain {
				return err
			}
			// Another site of the tenant whose Node or PM2 has gone missing is that
			// site's problem, which doctor reports; it must not stop this one.
			m.Log.Warn("leaving a site out of its tenant's PM2 daemon", "domain", s.Domain, "err", err)
			continue
		}
		if have, ok := daemons[d.Key]; ok {
			have.Sites = append(have.Sites, s)
			continue
		}
		d.Sites = []*state.Site{s}
		daemons[d.Key] = d
	}

	keys := make([]string, 0, len(daemons))
	for k := range daemons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := m.Unit.InstallPM2Daemon(ctx, daemons[k], rb); err != nil {
			return err
		}
	}

	installed, err := m.Unit.PM2DaemonKeys(owner)
	if err != nil {
		return err
	}
	for _, k := range installed {
		if _, keep := daemons[k]; keep {
			continue
		}
		if err := m.Unit.RemovePM2Daemon(ctx, owner, k); err != nil {
			return err
		}
	}
	return nil
}

// usesPM2Daemon reports whether a site has, or may have had, a place in a tenant daemon.
// node and bun both: bun runs under PM2 when asked.
func usesPM2Daemon(s *state.Site) bool {
	return s != nil && s.Dynamic() && (s.Runtime == "node" || s.Runtime == "bun")
}

// PM2DaemonUnit names the tenant daemon a site's application runs in, or "" for a site
// PM2 does not supervise.
func (m *Manager) PM2DaemonUnit(s *state.Site) string {
	if !usesPM2Daemon(s) || !m.UsesPM2(s) {
		return ""
	}
	return runtime.PM2DaemonUnit(&runtime.Context{Cfg: m.Cfg, Site: s})
}

// WantedPM2Daemons is the set of daemon units the given sites need, for `doctor` to
// tell a daemon that is still in use from one left behind.
func (m *Manager) WantedPM2Daemons(sites []*state.Site) map[string]bool {
	wanted := map[string]bool{}
	for _, s := range sites {
		if u := m.PM2DaemonUnit(s); u != "" {
			wanted[u] = true
		}
	}
	return wanted
}

// RunsOwnPM2 reports whether a PM2 site's unit on disk is still the shape an earlier
// release wrote — a daemon of the site's own, Type=forking with a PIDFile — rather than
// a oneshot bound to its tenant's daemon. Such a site keeps working, and keeps costing a
// daemon, until `reconcile --fix` re-renders the unit and the site next restarts.
func (m *Manager) RunsOwnPM2(s *state.Site) bool {
	if m.PM2DaemonUnit(s) == "" {
		return false
	}
	body, err := system.ReadFileLimit(m.Cfg.UnitPath(s.Owner, s.Domain), 1<<20)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "BindsTo=ratline-pm2@") {
			return false
		}
	}
	return true
}
