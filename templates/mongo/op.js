// The one script ratline gives mongosh, run as `mongosh --nodb --quiet --file op.js`.
//
// Nothing in here is generated. Every value — the connection URI, the database, the
// username, the password, the role — arrives through the environment, so no operator
// input is ever interpolated into JavaScript. That is the point: the alternative,
// building an --eval string, means a username containing a quote can close the string
// and run whatever follows, as root, against a server holding every tenant's data.
// There is no escaping to get right here because there is no string to escape into.
//
// --nodb matters for the same reason. mongosh normally takes the connection string as
// its first argument, which would put the admin password in argv, and /proc/PID/cmdline
// is world-readable. With --nodb it starts unconnected and this script connects using
// RATLINE_MONGO_URI from the environment, which is readable only by the process owner.
//
// Output is one JSON object on stdout, always, including for failures — so the caller
// parses a result rather than scraping a shell's idea of an error.

/* global Mongo, EJSON, print, process, quit */

function fail(message, detail) {
    print(EJSON.stringify({ ok: false, error: String(message), detail: detail || '' }));
    quit(1);
}

function need(name) {
    const v = process.env[name];
    if (v === undefined || v === '') {
        fail('missing ' + name);
    }
    return v;
}

const op = need('RATLINE_MONGO_OP');
const uri = need('RATLINE_MONGO_URI');

let conn;
try {
    conn = new Mongo(uri);
} catch (e) {
    // The URI itself is never echoed: it carries the admin password.
    fail('cannot connect to the MongoDB server', String(e.message || e));
}

// The database a command runs against. Every operation below is scoped to one database
// except ping and list, which are properties of the server.
function target() {
    return conn.getDB(need('RATLINE_MONGO_DB'));
}

function ok(data) {
    const out = Object.assign({ ok: true }, data || {});
    print(EJSON.stringify(out));
    quit(0);
}

try {
    switch (op) {
        case 'ping': {
            const admin = conn.getDB('admin');
            const res = admin.runCommand({ ping: 1 });
            if (!res.ok) {
                fail('the server did not answer a ping', EJSON.stringify(res));
            }
            // The version and topology are worth reporting: `doctor` prints them, and a
            // standalone server behaves differently from a replica set for writes.
            let build = {};
            try {
                build = admin.runCommand({ buildInfo: 1 });
            } catch (e) {
                build = {};
            }
            let hello = {};
            try {
                hello = admin.runCommand({ hello: 1 });
            } catch (e) {
                hello = {};
            }
            ok({
                version: build.version || '',
                topology: hello.setName ? 'replicaSet:' + hello.setName : (hello.msg || 'standalone'),
                // Whether authentication is actually enforced. A server with auth off
                // accepts ratline's commands and would accept anyone else's too.
                auth_enabled: !!(hello.saslSupportedMechs || build.openssl || true) && authEnforced(admin),
            });
            break;
        }

        case 'listDatabases': {
            const res = conn.getDB('admin').runCommand({ listDatabases: 1, nameOnly: false });
            if (!res.ok) {
                fail('could not list databases', EJSON.stringify(res));
            }
            const dbs = (res.databases || []).map(function (d) {
                return { name: d.name, size_on_disk: d.sizeOnDisk || 0, empty: !!d.empty };
            });
            ok({ databases: dbs });
            break;
        }

        // MongoDB has no createDatabase: a database begins to exist when something is
        // written into it. Creating a collection is the least surprising way to make it
        // real, so that `db list` shows it and a backup has something to find. Without
        // this, a freshly created database is invisible until the application writes,
        // which reads as the create having silently failed.
        case 'createDatabase': {
            const db = target();
            const marker = process.env.RATLINE_MONGO_COLLECTION || 'ratline';
            const existing = db.getCollectionNames();
            if (existing.indexOf(marker) === -1) {
                const res = db.createCollection(marker);
                if (res && res.ok === 0) {
                    fail('could not create the initial collection', EJSON.stringify(res));
                }
            }
            ok({ database: db.getName(), collection: marker, collections: db.getCollectionNames() });
            break;
        }

        case 'dropDatabase': {
            const db = target();
            const res = db.dropDatabase();
            if (res && res.ok === 0) {
                fail('could not drop the database', EJSON.stringify(res));
            }
            ok({ dropped: db.getName() });
            break;
        }

        case 'listUsers': {
            const db = target();
            const res = db.runCommand({ usersInfo: 1 });
            if (!res.ok) {
                fail('could not list users', EJSON.stringify(res));
            }
            const users = (res.users || []).map(function (u) {
                return {
                    username: u.user,
                    auth_db: u.db,
                    roles: (u.roles || []).map(function (r) { return r.role + '@' + r.db; }),
                    mechanisms: u.mechanisms || [],
                };
            });
            ok({ users: users });
            break;
        }

        case 'createUser': {
            const db = target();
            const username = need('RATLINE_MONGO_USER');
            const password = need('RATLINE_MONGO_PASSWORD');
            const role = need('RATLINE_MONGO_ROLE');
            // Scoped to this database by construction. The role document names the
            // database explicitly rather than relying on the connection's context, so a
            // role can never be granted more widely than the caller asked for.
            const res = db.runCommand({
                createUser: username,
                pwd: password,
                roles: [{ role: role, db: db.getName() }],
            });
            if (!res.ok) {
                fail('could not create the user', EJSON.stringify(res));
            }
            ok({ username: username, auth_db: db.getName(), role: role });
            break;
        }

        case 'updatePassword': {
            const db = target();
            const username = need('RATLINE_MONGO_USER');
            const password = need('RATLINE_MONGO_PASSWORD');
            const res = db.runCommand({ updateUser: username, pwd: password });
            if (!res.ok) {
                fail('could not change the password', EJSON.stringify(res));
            }
            ok({ username: username, auth_db: db.getName() });
            break;
        }

        case 'updateRole': {
            const db = target();
            const username = need('RATLINE_MONGO_USER');
            const role = need('RATLINE_MONGO_ROLE');
            // Replaced rather than added to: `db user grant` is how an operator says
            // "this user should have exactly this role", and accumulating roles silently
            // is how a read-only user ends up able to write.
            const res = db.runCommand({
                updateUser: username,
                roles: [{ role: role, db: db.getName() }],
            });
            if (!res.ok) {
                fail('could not change the role', EJSON.stringify(res));
            }
            ok({ username: username, auth_db: db.getName(), role: role });
            break;
        }

        case 'dropUser': {
            const db = target();
            const username = need('RATLINE_MONGO_USER');
            const res = db.runCommand({ dropUser: username });
            if (!res.ok) {
                fail('could not remove the user', EJSON.stringify(res));
            }
            ok({ dropped: username, auth_db: db.getName() });
            break;
        }

        // Used by `db show` to report what a database actually holds, which is the
        // difference between "provisioned" and "in use".
        case 'stats': {
            const db = target();
            const res = db.runCommand({ dbStats: 1 });
            if (!res.ok) {
                fail('could not read the database statistics', EJSON.stringify(res));
            }
            ok({
                database: db.getName(),
                collections: res.collections || 0,
                objects: res.objects || 0,
                data_size: res.dataSize || 0,
                storage_size: res.storageSize || 0,
                indexes: res.indexes || 0,
                index_size: res.indexSize || 0,
                names: db.getCollectionNames(),
            });
            break;
        }

        default:
            fail('unknown operation: ' + op);
    }
} catch (e) {
    // A thrown error from the driver is still a result, reported as JSON so the caller
    // does not have to distinguish "the command failed" from "mongosh fell over".
    fail(e.codeName || 'the operation failed', String(e.message || e));
}

// authEnforced reports whether the server requires authentication.
//
// A server with auth disabled answers every command from anyone who can reach the port.
// ratline's commands would appear to succeed, and the credentials it creates would mean
// nothing — so this is worth knowing and worth saying out loud.
function authEnforced(admin) {
    try {
        const params = admin.runCommand({ getCmdLineOpts: 1 });
        if (params && params.parsed) {
            if (params.parsed.security && params.parsed.security.authorization) {
                return params.parsed.security.authorization === 'enabled';
            }
            if (params.parsed.auth === true) {
                return true;
            }
            // Atlas and other managed deployments refuse getCmdLineOpts but always
            // enforce auth; reaching here on such a server means the check is unavailable
            // rather than that auth is off.
            return false;
        }
    } catch (e) {
        // Unauthorised to ask means auth is on and this connection is not an admin —
        // which is itself the answer.
        return true;
    }
    return false;
}
