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
