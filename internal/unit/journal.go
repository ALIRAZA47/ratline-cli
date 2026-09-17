package unit

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// Journal namespaces: one per site, so that a tenant can read the journal of their own
// services and nothing else's.
//
// Every unit ratline renders for a site — the service, its workers, its jobs — carries
// LogNamespace=<slug>. Whatever those processes write to stdout, stderr or syslog is then
// collected by a systemd-journald@<slug> instance into /var/log/journal/<machine-id>.<slug>
// rather than into the shared system journal. journalctl reads it with --namespace=<slug>.
//
// The reason is who may read a journal. Access to the shared one is a group membership,
// adm or systemd-journal, and either is every unit on the box: sshd, the panel, every other
// tenant's application. There is no per-unit grant. A namespace of one site's own is a
// thing that can be handed to that site's tenant.
//
// The hand-over is a POSIX ACL rather than a group on the directory. systemd creates the
// directory for the journald instance (its LogsDirectory=) root-owned, and on every start
// it checks the owner and, if that has changed, puts the entire tree back to root:root —
// so a chgrp to the tenant's group would last until the next restart and take every file's
// group with it. A tree whose owner already matches is left alone, and chown never touches
// ACLs. So: the directory stays root's, with a default ACL that makes every file journald
// creates in it readable by the tenant's group, and the same entry on the files already
// there when the grant is first applied. Nobody is ever added to systemd-journal.

var (
	// journalRoot is where journald keeps persistent journals; each namespace is a
	// sibling of the machine's own directory, named <machine-id>.<namespace>.
	journalRoot = "/var/log/journal"
	// journaldConfDir holds journald@<namespace>.conf, the per-instance configuration.
	journaldConfDir = "/etc/systemd"
	machineIDPath   = "/etc/machine-id"
	// journaldTemplatePaths is where a systemd that knows LogNamespace= keeps the
	// per-namespace journald template. On a systemd without it the directive would be
	// unknown and, worse, the units would depend on socket units that do not exist and
	// never start — so a site is only given a namespace when the template is there.
	journaldTemplatePaths = []string{
		"/lib/systemd/system/systemd-journald@.service",
		"/usr/lib/systemd/system/systemd-journald@.service",
	}
)

// JournalNamespace is the journald namespace a site's units log into: the site's slug,
// which is already unique per site and already a valid unit-name component.
func JournalNamespace(site *state.Site) string { return site.Slug }

// JournalNamespacesSupported reports whether this systemd has per-namespace journald
// instances, which is what LogNamespace= needs to mean anything.
func JournalNamespacesSupported() bool {
	for _, p := range journaldTemplatePaths {
		if system.Exists(p) {
			return true
		}
	}
	return false
}

// logNamespaceFor is the LogNamespace= value a rendered unit carries: the site's, or none
// on a systemd that cannot honour it.
func (m *Manager) logNamespaceFor(site *state.Site) string {
	if !JournalNamespacesSupported() {
		return ""
	}
	return JournalNamespace(site)
}

// MachineID reads /etc/machine-id, which is what journald names its directories after.
func MachineID() (string, error) {
	raw, err := os.ReadFile(machineIDPath)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s", machineIDPath).
			WithHint("journald names its directories after the machine id; systemd-machine-id-setup writes one")
	}
	id := strings.TrimSpace(string(raw))
	if _, err := hex.DecodeString(id); err != nil || len(id) != 32 {
		return "", rlerr.Preconditionf("%s does not hold a machine id: %q", machineIDPath, id)
	}
	return id, nil
}

// JournalNamespaceDir is the directory journald writes a namespace into.
func JournalNamespaceDir(namespace string) (string, error) {
	id, err := MachineID()
	if err != nil {
		return "", err
	}
	return filepath.Join(journalRoot, id+"."+namespace), nil
}

// journaldConfPath is the per-instance journald configuration for a namespace.
func journaldConfPath(namespace string) string {
	return filepath.Join(journaldConfDir, "journald@"+namespace+".conf")
}

// EnsureJournalNamespace prepares a site's journal namespace before the first unit that
// logs into it starts: the journald instance's configuration, and the directory with the
// tenant's read grant already on it — so the very first file journald creates is readable,
// not only the ones after a reconcile.
//
// Safe to run twice. Run from every path that installs a unit for the site, so that a
// unit which names the namespace can never be installed without it.
func (m *Manager) EnsureJournalNamespace(ctx context.Context, site *state.Site) error {
	_ = ctx
	if !JournalNamespacesSupported() {
		m.Log.Debug("this systemd has no per-namespace journald; the site's units log into the shared journal",
			"site", site.Domain)
		return nil
	}
	ns := JournalNamespace(site)
	id, err := system.LookupIdentity(site.Owner)
	if err != nil {
		// The same posture as the nginx log directory: under --dry-run the owner may not
		// exist yet, and there is nothing to grant to.
		m.Log.Debug("no system user for the site owner; not preparing its journal namespace",
			"owner", site.Owner, "err", err)
		return nil
	}
	dir, err := JournalNamespaceDir(ns)
	if err != nil {
		return err
	}
	conf := journaldConfPath(ns)
	if m.DryRun {
		m.Log.Info("would give the site its own journal namespace, readable by its tenant",
			"namespace", ns, "path", dir, "config", conf)
		return nil
	}

	body, err := m.renderJournaldConf(site, ns)
	if err != nil {
		return err
	}
	if system.Exists(conf) {
		managed, err := system.HasManagedHeader(conf)
		if err != nil {
			return err
		}
		if !managed {
			return rlerr.Preconditionf("%s exists but was not written by ratline", conf).
				WithHint("move it aside; settings of your own belong in journald@%s.conf.d/, which ratline never touches", ns)
		}
	}
	if err := system.WriteFileAtomic(conf, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}

	// The directory, before systemd makes it. Created the way systemd's LogsDirectory=
	// would (2755, root's), so that when the journald instance starts systemd finds an
	// owner it agrees with and leaves the tree — and the ACL on it — alone.
	if !system.Exists(journalRoot) {
		// journald's own parent. Its absence means the machine's journal is volatile;
		// systemd creates this directory itself the moment a namespace starts, so it is
		// going to exist either way. Made here as tmpfiles.d/systemd.conf specifies it,
		// rather than as a bare mkdir_parents would.
		gid := system.KeepUnchanged
		if g, err := system.LookupGroupID("systemd-journal"); err == nil && os.Geteuid() == 0 {
			gid = g
		}
		if _, err := system.EnsureDir(journalRoot, 0o755|os.ModeSetgid, system.KeepUnchanged, gid); err != nil {
			return err
		}
		m.Log.Info("created journald's persistent storage directory, which the machine's own journal will use from its next restart",
			"path", journalRoot)
	}
	if _, err := system.EnsureDir(dir, 0o755|os.ModeSetgid, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	d, err := system.OpenDirNoFollow(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if fi, err := d.Stat(); err == nil && fi.Mode()&(os.ModePerm|os.ModeSetgid) != 0o755|os.ModeSetgid {
		if err := d.Chmod(0o755 | os.ModeSetgid); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "setting the mode of %s", dir)
		}
	}
	if err := system.SetDefaultGroupRead(d, id.GID); err != nil {
		if rlerr.Is(err, rlerr.CodePrecondition) {
			// No ACL support on this filesystem. The site still works and still logs;
			// only the tenant's view is missing, and doctor says so.
			m.Log.Warn("the tenant will not be able to read the site's journal", "path", dir, "err", err)
			return nil
		}
		return err
	}
	// Files journald wrote before the grant, if the namespace predates it.
	entries, err := d.ReadDir(-1)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "listing %s", dir)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		// journald runs as the same user ratline does: root. A file in here that is
		// anybody else's was not written by journald and is not ratline's to open.
		f, err := system.OpenFileNoFollow(filepath.Join(dir, e.Name()), os.Geteuid())
		if err != nil {
			return err
		}
		err = system.GrantGroupRead(f, id.GID)
		f.Close()
		if err != nil {
			return err
		}
	}
	m.Log.Debug("the site's journal namespace is readable by its tenant", "namespace", ns, "group", id.GID)
	return nil
}

// JournalReadable reports whether a site's tenant can read the site's journal namespace,
// with the reason when not. A namespace that does not exist yet — no unit has started
// since it was configured — is not a problem, and neither is a systemd without namespaces.
func (m *Manager) JournalReadable(site *state.Site) (ok bool, detail string) {
	if !JournalNamespacesSupported() {
		return true, ""
	}
	dir, err := JournalNamespaceDir(JournalNamespace(site))
	if err != nil || !system.Exists(dir) {
		return true, ""
	}
	id, err := system.LookupIdentity(site.Owner)
	if err != nil {
		return true, ""
	}
	d, err := system.OpenDirNoFollow(dir)
	if err != nil {
		return false, "the journal directory " + dir + " could not be opened: " + err.Error()
	}
	defer d.Close()
	granted, err := system.DefaultACLGrantsGroupRead(d, id.GID)
	switch {
	case err != nil:
		return false, "the journal directory " + dir + " could not be checked: " + err.Error()
	case !granted:
		return false, "the tenant " + site.Owner + " cannot read the site's journal at " + dir
	}
	return true, ""
}

// RemoveJournalNamespace stops a deleted site's journald instance and, with purge, removes
// what it wrote and the configuration it ran under. Without purge the journal is kept
// alongside the site directory, for the post-mortem the operator may still want.
func (m *Manager) RemoveJournalNamespace(ctx context.Context, site *state.Site, purge bool) error {
	if !JournalNamespacesSupported() {
		return nil
	}
	ns := JournalNamespace(site)
	conf := journaldConfPath(ns)
	dir, dirErr := JournalNamespaceDir(ns)
	if m.DryRun {
		m.Log.Info("would stop the site's journald instance", "namespace", ns)
		if purge && dirErr == nil {
			m.Log.Info("would remove the site's journal", "path", dir, "config", conf)
		}
		return nil
	}
	// The instance is socket-activated, so the sockets go first or the stop is undone by
	// the next connection. None of the three may exist yet — a site whose units never
	// started, or a systemd old enough not to have the varlink socket — and 5 is what
	// systemctl says about a unit it does not have.
	for _, u := range []string{
		"systemd-journald@" + ns + ".socket",
		"systemd-journald-varlink@" + ns + ".socket",
		"systemd-journald@" + ns + ".service",
	} {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"stop", u},
			Mutates: true, OKExit: []int{1, 5}, Label: "stop " + u,
		}); err != nil {
			m.Log.Debug("systemctl reported a problem stopping the journald instance, continuing", "unit", u, "err", err)
		}
	}
	if !purge {
		return nil
	}
	if system.Exists(conf) {
		if err := system.RemoveManaged(conf); err != nil {
			return err
		}
	}
	if dirErr != nil {
		return dirErr
	}
	if system.Exists(dir) {
		if err := os.RemoveAll(dir); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", dir)
		}
	}
	return nil
}

// LogNamespaceOf reads the namespace a unit on disk logs into, or "" for the shared
// journal — a unit rendered before namespaces existed, or on a systemd without them.
//
// Read from the file rather than assumed from the site, because it is the file that
// decides where the lines went: a site reconciled into a namespace but not yet restarted
// is still logging where its running unit says.
func LogNamespaceOf(unitPath string) string {
	body, err := system.ReadFileLimit(unitPath, 1<<20)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "LogNamespace="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// journalArgs is the journalctl filter for a unit's log as root reads it: the unit,
// plus — when the unit on disk logs into a namespace — that namespace merged with the
// shared journal, so lines written before the site moved into its namespace still show.
func (m *Manager) journalArgs(unitName string) []string {
	args := []string{"-u", unitName}
	if ns := LogNamespaceOf(filepath.Join(m.Cfg.Paths.SystemdDir, unitName)); ns != "" {
		args = append(args, "--namespace=+"+ns)
	}
	return args
}

// JournalHint is the journalctl command an operator would type to read a unit's recent
// output, for error hints.
func (m *Manager) JournalHint(unitName string) string {
	return "journalctl " + strings.Join(m.journalArgs(unitName), " ") + " -n 50 --no-pager"
}

// JournalctlArgs is the argument list for reading a unit's log, ready for journalctl.
//
// For root the namespace is merged with the shared journal. For the tenant it is the
// namespace alone: the shared journal is not theirs to read, and asking journalctl for
// both prints a permissions hint in the middle of their log.
func (m *Manager) JournalctlArgs(unitName string, lines int, follow, asTenant bool) ([]string, error) {
	args := []string{"-u", unitName, "-n", strconv.Itoa(lines), "--no-pager", "--output=short-iso"}
	ns := LogNamespaceOf(filepath.Join(m.Cfg.Paths.SystemdDir, unitName))
	switch {
	case asTenant && ns == "":
		return nil, rlerr.Preconditionf("the service %s logs into the shared system journal, which only root can read", unitName).
			WithHint("the operator moves it into its own namespace with 'ratline reconcile --fix' followed by a restart of the site")
	case asTenant:
		args = append(args, "--namespace="+ns, "--quiet")
	case ns != "":
		args = append(args, "--namespace=+"+ns)
	}
	if follow {
		args = append(args, "--follow")
	}
	return args, nil
}

func (m *Manager) renderJournaldConf(site *state.Site, ns string) ([]byte, error) {
	tmpl, err := template.New("journald-namespace.conf.tmpl").ParseFS(templates.FS, "systemd/journald-namespace.conf.tmpl")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the journald template")
	}
	maxUse := m.Cfg.Defaults.JournalMaxUse
	if maxUse == "" {
		maxUse = "256M"
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"Domain":      site.Domain,
		"Namespace":   ns,
		"MaxUse":      maxUse,
		"GeneratedAt": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the journald configuration for %s", site.Domain)
	}
	return buf.Bytes(), nil
}
