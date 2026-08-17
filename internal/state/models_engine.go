package state

import "time"

// The engine-scoped database records, used by the SQL and key-value engines (MySQL now,
// Redis later). MongoDB predates the multi-engine surface and keeps the older
// Database/DatabaseUser/DatabaseAttachment types on its own tables; these carry the engine
// explicitly so one server can hold, say, a MySQL "shop" and a Redis "shop" at once.
//
// As with MongoDB, no password is ever stored: the engine keeps a hash and will not return
// it, so a lost password is rotated, not recovered.

// EngineDatabase is one database (or, for Redis, one keyspace namespace) recorded for a
// non-Mongo engine.
type EngineDatabase struct {
	Engine    string    `json:"engine"`
	Name      string    `json:"name"`
	Owner     string    `json:"owner"`
	Server    string    `json:"server,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	Notes     string    `json:"notes,omitempty"`

	// Users is filled by the queries that join; it is not a column.
	Users []*EngineUser `json:"users,omitempty"`
}

// EngineUser is a user scoped to one database. Scope generalizes MongoDB's auth_db: for
// MySQL it is the user's host (e.g. "%" or "localhost"); for Redis it will be the keyspace
// prefix the ACL user is confined to.
type EngineUser struct {
	Engine    string    `json:"engine"`
	Username  string    `json:"username"`
	Scope     string    `json:"scope,omitempty"`
	Database  string    `json:"database"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	RotatedAt time.Time `json:"rotated_at,omitempty"`

	Attachments []*EngineAttachment `json:"attachments,omitempty"`
}

// EngineAttachment records that a site was given one engine user's connection string under
// one environment key.
type EngineAttachment struct {
	Engine     string    `json:"engine"`
	Domain     string    `json:"domain"`
	Username   string    `json:"username"`
	Scope      string    `json:"scope,omitempty"`
	EnvKey     string    `json:"env_key"`
	AttachedAt time.Time `json:"attached_at"`
}

// EngineAccess is one address admitted to a non-Mongo engine's port by `db access allow`.
// The address is a canonical CIDR — the exact string the ufw rule was written with.
type EngineAccess struct {
	Engine    string    `json:"engine"`
	Address   string    `json:"address"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}
