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
| `runtimes` | Default node and python versions, the node process manager, mirrors, timeouts |
| `acme` | Contact address, directory URLs, key type, renewal window, the CA's rate limits |
| `ports` | The allocation window for sites that listen on TCP |
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
  python_default: "3.12"

acme:
  email: admin@example.com
  renew_before_days: 30

users:
  allow_sudo: false             # turning this on only permits the escape hatch to exist
```

## `features`

Off by default, each for a stated reason:

```yaml
features:
  db_provisioning: false     # `ratline db` is a stub until this lands
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
