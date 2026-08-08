package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const siteColumns = `domain, owner, runtime, slug, enabled,
	doc_root, spa, index_file,
	entry, node_version, package_manager, listen, process_manager, port, instances,
	app_module, python_version, asgi, app_server, workers, requirements, manage_py, static_url, static_dir,
	start_command, install_command, build_command, build_output, public_dir, repo, branch,
	pre_deploy_command, post_deploy_command,
	memory_max, cpu_quota, client_max_body_size, www_redirect, hsts, relaxed,
	created_at, updated_at, created_by, last_deploy_at`

func scanSite(row interface{ Scan(...any) error }) (*Site, error) {
	var (
		s                                   Site
		enabled, spa, asgi, hsts            int
		relaxed, created, updated, deployed string
	)
	err := row.Scan(&s.Domain, &s.Owner, &s.Runtime, &s.Slug, &enabled,
		&s.DocRoot, &spa, &s.IndexFile,
		&s.Entry, &s.NodeVersion, &s.PackageManager, &s.Listen, &s.ProcessManager, &s.Port, &s.Instances,
		&s.AppModule, &s.PythonVersion, &asgi, &s.AppServer, &s.Workers, &s.Requirements,
		&s.ManagePy, &s.StaticURL, &s.StaticDir,
		&s.StartCommand, &s.InstallCommand, &s.BuildCommand, &s.BuildOutput, &s.PublicDir, &s.Repo, &s.Branch,
		&s.PreDeployCommand, &s.PostDeployCommand,
		&s.MemoryMax, &s.CPUQuota, &s.ClientMaxBodySize, &s.WWWRedirect, &hsts, &relaxed,
		&created, &updated, &s.CreatedBy, &deployed)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabled == 1
	s.SPA = spa == 1
	s.ASGI = asgi == 1
	s.HSTS = hsts == 1
	s.Relaxed = splitList(relaxed)
	s.CreatedAt = parseTime(created)
	s.UpdatedAt = parseTime(updated)
	s.LastDeployAt = parseTime(deployed)
	return &s, nil
}

// PutSite inserts or updates a site along with its aliases, in one transaction.
func (s *Store) PutSite(ctx context.Context, site *Site) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sites (`+siteColumns+`)
			VALUES (?,?,?,?,?, ?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?, ?,?, ?,?,?,?,?,?, ?,?,?,?)
			ON CONFLICT(domain) DO UPDATE SET
				owner=excluded.owner, runtime=excluded.runtime, slug=excluded.slug, enabled=excluded.enabled,
				doc_root=excluded.doc_root, spa=excluded.spa, index_file=excluded.index_file,
				entry=excluded.entry, node_version=excluded.node_version,
				package_manager=excluded.package_manager, listen=excluded.listen,
				process_manager=excluded.process_manager,
				port=excluded.port, instances=excluded.instances,
				app_module=excluded.app_module, python_version=excluded.python_version,
				asgi=excluded.asgi, app_server=excluded.app_server, workers=excluded.workers,
				requirements=excluded.requirements, manage_py=excluded.manage_py,
				static_url=excluded.static_url, static_dir=excluded.static_dir,
				start_command=excluded.start_command, install_command=excluded.install_command,
				build_command=excluded.build_command, build_output=excluded.build_output,
				public_dir=excluded.public_dir, repo=excluded.repo, branch=excluded.branch,
				pre_deploy_command=excluded.pre_deploy_command,
				post_deploy_command=excluded.post_deploy_command,
				memory_max=excluded.memory_max, cpu_quota=excluded.cpu_quota,
				client_max_body_size=excluded.client_max_body_size,
				www_redirect=excluded.www_redirect, hsts=excluded.hsts, relaxed=excluded.relaxed,
				updated_at=excluded.updated_at, last_deploy_at=excluded.last_deploy_at`,
			site.Domain, site.Owner, site.Runtime, site.Slug, boolToInt(site.Enabled),
			site.DocRoot, boolToInt(site.SPA), site.IndexFile,
			site.Entry, site.NodeVersion, site.PackageManager, site.Listen, site.ProcessManager, site.Port, site.Instances,
			site.AppModule, site.PythonVersion, boolToInt(site.ASGI), site.AppServer, site.Workers,
			site.Requirements, site.ManagePy, site.StaticURL, site.StaticDir,
			site.StartCommand, site.InstallCommand, site.BuildCommand, site.BuildOutput,
			site.PublicDir, site.Repo, site.Branch,
			site.PreDeployCommand, site.PostDeployCommand,
			site.MemoryMax, site.CPUQuota, site.ClientMaxBodySize, site.WWWRedirect,
			boolToInt(site.HSTS), joinList(site.Relaxed),
			orNow(formatTime(site.CreatedAt)), now(), site.CreatedBy, formatTime(site.LastDeployAt))
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the site %s", site.Domain)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM aliases WHERE domain = ?`, site.Domain); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "replacing the aliases for %s", site.Domain)
		}
		for _, a := range site.Aliases {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO aliases (alias, domain, created_at) VALUES (?,?,?)`, a, site.Domain, now())
			if err != nil {
				// A UNIQUE violation here means another site already claims the
				// alias, which would make two vhosts fight over one name.
				if strings.Contains(err.Error(), "UNIQUE") {
					return rlerr.Preconditionf("the alias %s is already used by another site", a).
						WithHint("run 'ratline site list' to find it, or remove the alias there first")
				}
				return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the alias %s", a)
			}
		}
		return nil
	})
}

// GetSite looks up one site with its aliases.
func (s *Store) GetSite(ctx context.Context, domain string) (*Site, error) {
	site, err := scanSite(s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE domain = ?`, domain))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("site", domain)
	}
	if err != nil {
		return nil, scanError(err, "sites")
	}
	if site.Aliases, err = s.aliasesFor(ctx, domain); err != nil {
		return nil, err
	}
	return site, nil
}

// FindSiteByName resolves a domain or one of its aliases to a site. An operator
// who types the www name should not have to remember which one is canonical.
func (s *Store) FindSiteByName(ctx context.Context, name string) (*Site, error) {
	site, err := s.GetSite(ctx, name)
	if err == nil {
		return site, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var domain string
	qerr := s.db.QueryRowContext(ctx, `SELECT domain FROM aliases WHERE alias = ?`, name).Scan(&domain)
	if errors.Is(qerr, sql.ErrNoRows) {
		return nil, notFound("site", name)
	}
	if qerr != nil {
		return nil, scanError(qerr, "aliases")
	}
	return s.GetSite(ctx, domain)
}

// SiteFilter narrows a listing.
type SiteFilter struct {
	Owner   string
	Runtime string
}

// ListSites returns sites matching the filter, ordered by domain.
func (s *Store) ListSites(ctx context.Context, f SiteFilter) ([]*Site, error) {
	q := `SELECT ` + siteColumns + ` FROM sites`
	var (
		where []string
		args  []any
	)
	if f.Owner != "" {
		where = append(where, "owner = ?")
		args = append(args, f.Owner)
	}
	if f.Runtime != "" {
		where = append(where, "runtime = ?")
		args = append(args, f.Runtime)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY domain"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, scanError(err, "sites")
	}
	defer rows.Close()
	var out []*Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, scanError(err, "sites")
		}
		out = append(out, site)
	}
	if err := rows.Err(); err != nil {
		return nil, scanError(err, "sites")
	}
	for _, site := range out {
		if site.Aliases, err = s.aliasesFor(ctx, site.Domain); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) aliasesFor(ctx context.Context, domain string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias FROM aliases WHERE domain = ? ORDER BY alias`, domain)
	if err != nil {
		return nil, scanError(err, "aliases")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, scanError(err, "aliases")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// NameInUse reports whether a domain or alias is already claimed, and by which
// site. Two vhosts claiming one server_name is a configuration ratline refuses
// to create.
func (s *Store) NameInUse(ctx context.Context, name string) (string, bool, error) {
	var domain string
	err := s.db.QueryRowContext(ctx, `
		SELECT domain FROM sites WHERE domain = ?
		UNION ALL
		SELECT domain FROM aliases WHERE alias = ?
		LIMIT 1`, name, name).Scan(&domain)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, scanError(err, "sites")
	}
	return domain, true, nil
}

// SetSiteEnabled records a site being enabled or disabled.
func (s *Store) SetSiteEnabled(ctx context.Context, domain string, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sites SET enabled = ?, updated_at = ? WHERE domain = ?`, boolToInt(enabled), now(), domain)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the site %s", domain)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("site", domain)
	}
	return nil
}

// TouchDeploy records the time of a successful deploy.
func (s *Store) TouchDeploy(ctx context.Context, domain string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sites SET last_deploy_at = ?, updated_at = ? WHERE domain = ?`, now(), now(), domain)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the site %s", domain)
	}
	return nil
}

// DeleteSite removes a site and its aliases.
func (s *Store) DeleteSite(ctx context.Context, domain string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM aliases WHERE domain = ?`,
			`DELETE FROM cert_attachments WHERE domain = ?`,
			`DELETE FROM ports WHERE domain = ?`,
			`DELETE FROM sites WHERE domain = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, domain); err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the site %s", domain)
			}
		}
		return nil
	})
}

// SlugInUse reports whether a slug is taken, which is how unit-name collisions
// are caught before two sites share one systemd unit.
func (s *Store) SlugInUse(ctx context.Context, slug, exceptDomain string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sites WHERE slug = ? AND domain <> ?`, slug, exceptDomain).Scan(&n)
	if err != nil {
		return false, scanError(err, "sites")
	}
	return n > 0, nil
}
