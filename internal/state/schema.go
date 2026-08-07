package state

// migrations is an append-only list. Each entry is applied in one transaction
// and its index plus one is its version number. Never edit a released
// migration: add a new one, so that a server upgrading from any version reaches
// the same schema as a fresh install.
var migrations = [][]string{
	// 1 — the initial schema.
	{
		`CREATE TABLE users (
			name            TEXT    PRIMARY KEY,
			uid             INTEGER NOT NULL DEFAULT 0,
			gid             INTEGER NOT NULL DEFAULT 0,
			home            TEXT    NOT NULL,
			shell           TEXT    NOT NULL,
			comment         TEXT    NOT NULL DEFAULT '',
			quota           TEXT    NOT NULL DEFAULT '',
			memory_max      TEXT    NOT NULL DEFAULT '',
			sftp_only       INTEGER NOT NULL DEFAULT 0,
			password_login  INTEGER NOT NULL DEFAULT 0,
			disabled        INTEGER NOT NULL DEFAULT 0,
			created_at      TEXT    NOT NULL,
			updated_at      TEXT    NOT NULL,
			created_by      TEXT    NOT NULL DEFAULT ''
		)`,

		`CREATE TABLE sites (
			domain               TEXT    PRIMARY KEY,
			owner                TEXT    NOT NULL REFERENCES users(name) ON DELETE CASCADE,
			runtime              TEXT    NOT NULL,
			slug                 TEXT    NOT NULL UNIQUE,
			enabled              INTEGER NOT NULL DEFAULT 1,

			-- static
			doc_root             TEXT    NOT NULL DEFAULT 'public',
			spa                  INTEGER NOT NULL DEFAULT 0,
			index_file           TEXT    NOT NULL DEFAULT 'index.html',

			-- node
			entry                TEXT    NOT NULL DEFAULT '',
			node_version         TEXT    NOT NULL DEFAULT '',
			package_manager      TEXT    NOT NULL DEFAULT '',
			listen               TEXT    NOT NULL DEFAULT 'socket',
			port                 INTEGER NOT NULL DEFAULT 0,
			instances            INTEGER NOT NULL DEFAULT 1,

			-- python
			app_module           TEXT    NOT NULL DEFAULT '',
			python_version       TEXT    NOT NULL DEFAULT '',
			asgi                 INTEGER NOT NULL DEFAULT 0,
			app_server           TEXT    NOT NULL DEFAULT '',
			workers              INTEGER NOT NULL DEFAULT 0,
			requirements         TEXT    NOT NULL DEFAULT '',
			manage_py            TEXT    NOT NULL DEFAULT '',
			static_url           TEXT    NOT NULL DEFAULT '',
			static_dir           TEXT    NOT NULL DEFAULT '',

			-- shared
			start_command        TEXT    NOT NULL DEFAULT '',
			install_command      TEXT    NOT NULL DEFAULT '',
			build_command        TEXT    NOT NULL DEFAULT '',
			build_output         TEXT    NOT NULL DEFAULT '',
			public_dir           TEXT    NOT NULL DEFAULT '',
			repo                 TEXT    NOT NULL DEFAULT '',
			branch               TEXT    NOT NULL DEFAULT '',

			-- limits and vhost options
			memory_max           TEXT    NOT NULL DEFAULT '',
			cpu_quota            TEXT    NOT NULL DEFAULT '',
			client_max_body_size TEXT    NOT NULL DEFAULT '',
			www_redirect         TEXT    NOT NULL DEFAULT 'none',
			hsts                 INTEGER NOT NULL DEFAULT 0,
			-- systemd hardening directives deliberately relaxed for this site
			relaxed              TEXT    NOT NULL DEFAULT '',

			created_at           TEXT    NOT NULL,
			updated_at           TEXT    NOT NULL,
			created_by           TEXT    NOT NULL DEFAULT '',
			last_deploy_at       TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX sites_owner ON sites(owner)`,
		`CREATE INDEX sites_runtime ON sites(runtime)`,

		`CREATE TABLE aliases (
			alias      TEXT PRIMARY KEY,
			domain     TEXT NOT NULL REFERENCES sites(domain) ON DELETE CASCADE,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX aliases_domain ON aliases(domain)`,

		`CREATE TABLE ssh_keys (
			id            TEXT    PRIMARY KEY,
			label         TEXT    NOT NULL,
			fingerprint   TEXT    NOT NULL,
			algorithm     TEXT    NOT NULL,
			bits          INTEGER NOT NULL DEFAULT 0,
			blob          TEXT    NOT NULL,
			comment       TEXT    NOT NULL DEFAULT '',
			scope         TEXT    NOT NULL,
			owner         TEXT    NOT NULL DEFAULT '',
			site          TEXT    NOT NULL DEFAULT '',
			options       TEXT    NOT NULL DEFAULT '',
			source        TEXT    NOT NULL DEFAULT 'manual',
			allow_shell   INTEGER NOT NULL DEFAULT 0,
			sftp_only     INTEGER NOT NULL DEFAULT 0,
			from_cidr     TEXT    NOT NULL DEFAULT '',
			command       TEXT    NOT NULL DEFAULT '',
			added_at      TEXT    NOT NULL,
			added_by      TEXT    NOT NULL DEFAULT '',
			expires_at    TEXT    NOT NULL DEFAULT '',
			last_used_at  TEXT    NOT NULL DEFAULT '',
			last_used_ip  TEXT    NOT NULL DEFAULT '',
			revoked_at    TEXT    NOT NULL DEFAULT '',
			UNIQUE (fingerprint, scope, owner, site)
		)`,
		`CREATE INDEX ssh_keys_fingerprint ON ssh_keys(fingerprint)`,
		`CREATE INDEX ssh_keys_scope ON ssh_keys(scope, owner, site)`,

		`CREATE TABLE key_usage (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			fingerprint TEXT NOT NULL,
			used_at     TEXT NOT NULL,
			remote_ip   TEXT NOT NULL DEFAULT '',
			method      TEXT NOT NULL DEFAULT '',
			UNIQUE (fingerprint, used_at, remote_ip)
		)`,
		`CREATE INDEX key_usage_fingerprint ON key_usage(fingerprint)`,

		`CREATE TABLE certificates (
			name                 TEXT    PRIMARY KEY,
			lineage              TEXT    NOT NULL DEFAULT '',
			source               TEXT    NOT NULL,
			issuer               TEXT    NOT NULL DEFAULT '',
			serial               TEXT    NOT NULL DEFAULT '',
			fingerprint          TEXT    NOT NULL DEFAULT '',
			key_type             TEXT    NOT NULL DEFAULT '',
			not_before           TEXT    NOT NULL DEFAULT '',
			not_after            TEXT    NOT NULL DEFAULT '',
			challenge            TEXT    NOT NULL DEFAULT '',
			dns_provider         TEXT    NOT NULL DEFAULT '',
			auto_renew           INTEGER NOT NULL DEFAULT 1,
			cert_path            TEXT    NOT NULL DEFAULT '',
			key_path             TEXT    NOT NULL DEFAULT '',
			chain_path           TEXT    NOT NULL DEFAULT '',
			last_renewal_at      TEXT    NOT NULL DEFAULT '',
			last_renewal_status  TEXT    NOT NULL DEFAULT '',
			last_renewal_error   TEXT    NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at           TEXT    NOT NULL,
			updated_at           TEXT    NOT NULL
		)`,

		`CREATE TABLE cert_sans (
			cert TEXT NOT NULL REFERENCES certificates(name) ON DELETE CASCADE,
			san  TEXT NOT NULL,
			PRIMARY KEY (cert, san)
		)`,

		// Many-to-many, so one SAN certificate can serve several vhosts.
		`CREATE TABLE cert_attachments (
			cert        TEXT NOT NULL REFERENCES certificates(name) ON DELETE CASCADE,
			domain      TEXT NOT NULL,
			attached_at TEXT NOT NULL,
			PRIMARY KEY (cert, domain)
		)`,
		`CREATE INDEX cert_attachments_domain ON cert_attachments(domain)`,

		// The backing store for rate-limit budgeting.
		`CREATE TABLE acme_attempts (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			registered_domain TEXT    NOT NULL,
			domain            TEXT    NOT NULL,
			san_set           TEXT    NOT NULL DEFAULT '',
			attempted_at      TEXT    NOT NULL,
			outcome           TEXT    NOT NULL,
			error_class       TEXT    NOT NULL DEFAULT '',
			staging           INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX acme_attempts_domain ON acme_attempts(registered_domain, attempted_at)`,

		`CREATE TABLE ports (
			port         INTEGER PRIMARY KEY,
			domain       TEXT    NOT NULL,
			allocated_at TEXT    NOT NULL
		)`,
		`CREATE INDEX ports_domain ON ports(domain)`,

		`CREATE TABLE deployments (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			domain       TEXT    NOT NULL,
			started_at   TEXT    NOT NULL,
			finished_at  TEXT    NOT NULL DEFAULT '',
			git_sha      TEXT    NOT NULL DEFAULT '',
			steps        TEXT    NOT NULL DEFAULT '',
			ok           INTEGER NOT NULL DEFAULT 0,
			health       TEXT    NOT NULL DEFAULT '',
			rolled_back  INTEGER NOT NULL DEFAULT 0,
			error        TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX deployments_domain ON deployments(domain, started_at)`,

		`CREATE TABLE events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			at          TEXT    NOT NULL,
			command     TEXT    NOT NULL,
			argv        TEXT    NOT NULL DEFAULT '',
			uid         INTEGER NOT NULL DEFAULT 0,
			sudo_user   TEXT    NOT NULL DEFAULT '',
			target      TEXT    NOT NULL DEFAULT '',
			result      TEXT    NOT NULL DEFAULT '',
			exit_code   INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			detail      TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX events_at ON events(at)`,
		`CREATE INDEX events_target ON events(target)`,

		`CREATE TABLE server (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	},

	// 2 — which process manager supervises a node site.
	//
	// A new migration rather than an edit to the one above: migration 1 has
	// shipped, and editing it would leave an upgraded server with a different
	// schema from a fresh install.
	{
		`ALTER TABLE sites ADD COLUMN process_manager TEXT NOT NULL DEFAULT ''`,
	},

	// 3 — MongoDB databases and their users.
	//
	// State is an index, not the truth: the truth is what the MongoDB server says, and
	// `db list --live` asks it. What state adds is ownership and intent — which tenant a
	// database belongs to, which site was given its URI, and which users ratline created
	// as opposed to which were already there. Without that, `user delete --purge` cannot
	// know what to revoke, and a shared cluster slowly fills with credentials nobody can
	// account for.
	{
		`CREATE TABLE databases (
			name        TEXT PRIMARY KEY,
			owner       TEXT NOT NULL,
			server      TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL,
			created_by  TEXT NOT NULL DEFAULT '',
			notes       TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (owner) REFERENCES users(name) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_databases_owner ON databases(owner)`,

		// A user belongs to the database it authenticates against, which for a
		// ratline-created user is always the database it has a role on. auth_db is
		// recorded anyway, because a user adopted from an existing cluster may
		// authenticate somewhere else and the URI has to say so.
		`CREATE TABLE database_users (
			username    TEXT NOT NULL,
			auth_db     TEXT NOT NULL,
			database    TEXT NOT NULL,
			role        TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			created_by  TEXT NOT NULL DEFAULT '',
			rotated_at  TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (username, auth_db),
			FOREIGN KEY (database) REFERENCES databases(name) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_database_users_database ON database_users(database)`,

		// Which site was handed which user's connection string. A site can be given a
		// database it does not own — a reporting job reading another tenant's data with a
		// read role — so this is its own table rather than a column.
		`CREATE TABLE database_attachments (
			domain      TEXT NOT NULL,
			username    TEXT NOT NULL,
			auth_db     TEXT NOT NULL,
			env_key     TEXT NOT NULL,
			attached_at TEXT NOT NULL,
			PRIMARY KEY (domain, env_key),
			FOREIGN KEY (domain) REFERENCES sites(domain) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_database_attachments_user ON database_attachments(username, auth_db)`,
	},

	// Extra units belonging to a site: scheduled jobs and long-running workers.
	//
	// One table with a kind rather than two, because everything about them is shared —
	// the tenant, the working directory, the environment, the sandbox, the resource
	// ceiling — and the only real difference is what starts them. A job is started by a
	// timer and expected to exit; a worker is started with the site and expected not to.
	//
	// Recorded in state rather than left in /etc/systemd, so that `status`, `doctor`,
	// `reconcile` and `export` can all see them. A tenant's crontab is invisible to every
	// one of those, which is the reason this is not a crontab.
	{
		`CREATE TABLE site_units (
			domain      TEXT NOT NULL,
			name        TEXT NOT NULL,
			kind        TEXT NOT NULL,
			command     TEXT NOT NULL,
			schedule    TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			enabled     INTEGER NOT NULL DEFAULT 1,
			persistent  INTEGER NOT NULL DEFAULT 0,
			timeout     TEXT NOT NULL DEFAULT '',
			instances   INTEGER NOT NULL DEFAULT 1,
			memory_max  TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			created_by  TEXT NOT NULL DEFAULT '',
			last_run_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (domain, name),
			FOREIGN KEY (domain) REFERENCES sites(domain) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_site_units_domain ON site_units(domain, kind)`,
	},
}
