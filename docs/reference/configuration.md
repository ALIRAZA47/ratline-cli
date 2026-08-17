# Configuration

`/etc/ratline/config.yaml`. Read on every invocation, so there is nothing to reload
after an edit.

The shipped file **is** the reference: every setting carries a comment explaining what
it does and why the default is what it is, and the values shown are the built-in
defaults. Delete anything you do not want to override — a partial file is a valid file.

An unknown key is an **error**, not a silently ignored typo. `paths.systemd_dir`
misspelled as `paths.systemdir` would otherwise mean ratline writing units somewhere
nobody looks.

```bash
sudo ratline init          # writes the file with every default and its comment
sudo ratline doctor        # validates it
```

## Sections

| Section | What it governs |
|---|---|
| `server` | Hostname and public addresses; the admin account for global keys |
| `paths` | Every directory ratline reads or writes — see [filesystem.md](filesystem.md) |
| `defaults` | Per-site defaults: umask, body size, timeouts, memory and CPU ceilings, HSTS |
| `users` | Reserved names, whether sudo may be granted at all, quota support, home modes |
| `ssh` | Key algorithm policy, minimum RSA bits, scope behaviour, verification, expiry pruning |
| `nginx` | Reload timeout, gzip and brotli, `server_tokens`, asset cache lifetime |
| `runtimes` | Default node, bun and python versions, the node process manager, mirrors, timeouts |
| `acme` | Contact address, directory URLs, key type, renewal window, the CA's rate limits |
| `ports` | The allocation window for sites that listen on TCP |
| `databases` | The MongoDB server `ratline db` provisions inside |
| `logging` | Level and colour |
| `features` | Opt-in behaviour that is off by default |

## The settings most often changed

```yaml
defaults:
  client_max_body_size: 20M     # the most common cause of a mystery 413
  memory_max: 512M              # per site; the kernel enforces it
  health_timeout: 30s           # how long a start waits for a real HTTP answer

runtimes:
  node_process_manager: pm2     # or: direct
  node_default: "22"
  bun_default: "1.2"
  python_default: "3.12"

acme:
  email: admin@example.com
  renew_before_days: 30

users:
  allow_sudo: false             # turning this on only permits the escape hatch to exist
```

## `databases`

```yaml
paths:
  mongo_uri_file: /etc/ratline/db/mongodb.uri   # 0600, root-owned

databases:
  mongodb:
    default_role: readWrite     # granted to a user created without --role
    env_key: MONGODB_URI        # the variable --attach writes
    timeout: 30s                # one mongosh invocation
    initial_collection: ratline # so a new database is visible to `db list`

features:
  db_provisioning: true         # off by default
```

The admin connection string is a file rather than a setting, held to the same rule as the
DNS provider credentials: it is the root password for every database on the server, and
`config.yaml` is a file operators paste into support tickets. ratline refuses to read it at
any mode that lets another account see it.

`default_role` is `readWrite` rather than `dbOwner` because an application reads and writes
its own collections and does not need to create users or drop the database it lives in.
Cluster-wide roles cannot be configured here at all — see
[the databases topic](../topics/databases.md) for why.

`timeout` is what turns an unreachable managed cluster into an error naming its access list.
A cluster that has not allowed this server's address does not refuse the connection; it
ignores it, so without a bound the command never returns.

## `features`

Off by default, each for a stated reason:

```yaml
features:
  db_provisioning: false     # turns on `ratline db`; needs a MongoDB admin URI
  strict_isolation: false    # adds a chroot and a bind mount to site-scoped SSH keys;
                             # off because a misconfigured chroot generates support tickets
```

## Rate limits are policy, not facts

`acme.rate_limits` mirrors the certificate authority's published limits so ratline can
refuse an attempt with a countdown instead of discovering the limit the hard way. They
do change. If a refusal looks wrong, check the CA's documentation and update the
numbers.

## A private certificate authority needs two settings

```yaml
acme:
  directory_url: https://ca.internal/acme/acme/directory
  ca_bundle: /etc/ssl/certs/internal-root.pem
```

`ca_bundle` is the one that is easy to miss, and missing it is not visible until a
certificate expires. certbot verifies the ACME server's own TLS certificate against
certifi's bundled roots rather than the system trust store, so a private root installed
with `update-ca-certificates` is not consulted.

`cert issue --acme-ca-bundle` covers one issuance. Renewal runs from a timer months
later with no command line and reads `ca_bundle` — so a certificate issued with the
flag alone can never renew. `ratline cert issue` warns when the flag is set and this is
empty. `ratline doctor` reports it as a problem against the certificate, and
`ratline doctor server` carries the same check under the name `acme-trust` — both read
the lineage's own renewal config, which is the file certbot reads, rather than
`directory_url`, which may have changed since the certificate was issued.

## `renew_before_days` is the window that decides

ratline holds the certificate state and the failure backoff, so once it judges a
certificate due it tells certbot to act. certbot's own 30-day window does not get a
second vote: before this was true, raising `renew_before_days` above 30 changed nothing
and said nothing.
