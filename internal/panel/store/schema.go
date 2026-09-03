package store

// migrations is an append-only list, applied in order, each in one transaction.
// Its index plus one is its version number.
//
// The same rule as ratline's own state database: never edit a released entry, add a
// new one. A panel upgrading from any version has to converge on the same schema as a
// fresh install, and the binary refuses to run against a database newer than it
// understands rather than writing rows the next version cannot read.
var migrations = [][]string{
	// 1 — accounts, sessions, invitations, the action log and the job queue.
	{
		// A panel account is not a system account and never becomes one. It authorises
		// somebody to ask the panel to run ratline; the tenant users ratline creates are
		// a different population entirely, and conflating them is how a panel login
		// turns into a shell.
		`CREATE TABLE accounts (
			id            TEXT    PRIMARY KEY,
			email         TEXT    NOT NULL UNIQUE,
			name          TEXT    NOT NULL DEFAULT '',
			role          TEXT    NOT NULL,
			password_hash TEXT    NOT NULL,
			totp_secret   TEXT    NOT NULL DEFAULT '',
			totp_enabled  INTEGER NOT NULL DEFAULT 0,
			disabled      INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT    NOT NULL,
			created_by    TEXT    NOT NULL DEFAULT '',
			updated_at    TEXT    NOT NULL,
			last_login_at TEXT    NOT NULL DEFAULT '',
			last_login_ip TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX accounts_role ON accounts(role)`,

		// Only the hash of a session token is stored. A panel database that leaks must
		// not hand over live sessions on top of everything else it reveals.
		//
		// The CSRF token beside it is stored in the clear, and that is not an
		// oversight. It is not a credential on its own: it is only useful to
		// somebody who also holds the session cookie, and anybody reading this
		// file already has the machine. It has to be readable because the browser
		// asks for it again on every page load — a token that could only be issued
		// once would leave a reloaded tab signed in and unable to do anything.
		`CREATE TABLE sessions (
			token_hash   TEXT    PRIMARY KEY,
			account_id   TEXT    NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			csrf_token   TEXT    NOT NULL,
			created_at   TEXT    NOT NULL,
			last_seen_at TEXT    NOT NULL,
			expires_at   TEXT    NOT NULL,
			ip           TEXT    NOT NULL DEFAULT '',
			user_agent   TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX sessions_account ON sessions(account_id)`,
		`CREATE INDEX sessions_expiry ON sessions(expires_at)`,

		`CREATE TABLE invites (
			id          TEXT    PRIMARY KEY,
			token_hash  TEXT    NOT NULL UNIQUE,
			email       TEXT    NOT NULL,
			role        TEXT    NOT NULL,
			invited_by  TEXT    NOT NULL DEFAULT '',
			created_at  TEXT    NOT NULL,
			expires_at  TEXT    NOT NULL,
			accepted_at TEXT    NOT NULL DEFAULT '',
			revoked_at  TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX invites_email ON invites(email)`,

		// What the panel did, separately from what ratline logged. ratline's audit
		// trail records the command; this records who asked for it, which is the half
		// ratline cannot know because every invocation reaches it as root.
		`CREATE TABLE actions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			at          TEXT    NOT NULL,
			actor_id    TEXT    NOT NULL DEFAULT '',
			actor       TEXT    NOT NULL DEFAULT '',
			action      TEXT    NOT NULL,
			argv        TEXT    NOT NULL DEFAULT '',
			target      TEXT    NOT NULL DEFAULT '',
			dry_run     INTEGER NOT NULL DEFAULT 0,
			ok          INTEGER NOT NULL DEFAULT 0,
			exit_code   INTEGER NOT NULL DEFAULT 0,
			error       TEXT    NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			ip          TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX actions_at ON actions(at)`,
		`CREATE INDEX actions_actor ON actions(actor_id, at)`,

		// Long-running work — a deploy, an issuance, a runtime build — outlives the
		// request that asked for it, so it is a row with a transcript rather than a
		// response body somebody has to keep a browser tab open for.
		`CREATE TABLE jobs (
			id          TEXT    PRIMARY KEY,
			action      TEXT    NOT NULL,
			target      TEXT    NOT NULL DEFAULT '',
			argv        TEXT    NOT NULL DEFAULT '',
			actor_id    TEXT    NOT NULL DEFAULT '',
			actor       TEXT    NOT NULL DEFAULT '',
			state       TEXT    NOT NULL,
			queued_at   TEXT    NOT NULL,
			started_at  TEXT    NOT NULL DEFAULT '',
			finished_at TEXT    NOT NULL DEFAULT '',
			exit_code   INTEGER NOT NULL DEFAULT 0,
			error       TEXT    NOT NULL DEFAULT '',
			hint        TEXT    NOT NULL DEFAULT '',
			output      TEXT    NOT NULL DEFAULT '',
			dry_run     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX jobs_queued ON jobs(queued_at)`,
		`CREATE INDEX jobs_state ON jobs(state)`,

		// Failed logins, so a password can be rate-limited per account and per address
		// without keeping that state in memory, where a restart would clear it.
		`CREATE TABLE login_attempts (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			at       TEXT NOT NULL,
			email    TEXT NOT NULL DEFAULT '',
			ip       TEXT NOT NULL DEFAULT '',
			ok       INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX login_attempts_at ON login_attempts(at)`,
		`CREATE INDEX login_attempts_email ON login_attempts(email, at)`,
		`CREATE INDEX login_attempts_ip ON login_attempts(ip, at)`,

		`CREATE TABLE settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	},
}
