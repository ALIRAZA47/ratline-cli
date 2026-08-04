# Giving someone access

Three scopes, and the scope is the grant:

```bash
# You, administering the server
sudo ratline key add --global --file ~/.ssh/id_ed25519.pub --label "my laptop"

# A tenant, for everything they own
sudo ratline key add --user acme --file client.pub --label "acme's laptop"

# CI, for one site, file transfer only
sudo ratline key add --site app.example.com --file ci.pub --label "github actions"
```

A site-scoped key gets sftp, rsync and git through a forced command — not a shell.
That is what it is for: a build system that uploads, or a designer who needs to
replace files. `--allow-shell` overrides it per key, with a warning, because it is a
materially different grant.

## Constraints worth using

```bash
sudo ratline key add --site app.example.com --file contractor.pub \
    --label "contractor" --from 203.0.113.0/24 --expires 2026-12-31
```

`--from` restricts the source network. `--expires` uses OpenSSH's `expiry-time=`
where the server supports it, and a daily timer prunes expired keys whether it does
or not — so the expiry is real on an older sshd too.

## Deploy keys, the other direction

```bash
sudo ratline site deploy-key app.example.com
```

That generates a key for the site to authenticate *to* your repository host, prints
the public half to paste there, and keeps the private half `0600` inside the site
directory. Site-scoped, so a compromised CI credential reaches one repository and
one site.

## Rotating and revoking

```bash
# Add the replacement, confirm it works, then remove the old one.
sudo ratline key add --user acme --file new.pub --label "acme's new laptop"
sudo ratline key remove <fingerprint>

# A compromised key: off every scope, and onto the revocation list, which sshd
# consults regardless of any authorized_keys file.
sudo ratline key revoke <fingerprint>

# What has gone stale?
sudo ratline key audit
```

Fingerprints complete on Tab, which is the only reason that first command is
typeable.

## What ratline will not do to sshd

`PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` and `Port` are never
changed without an explicit flag and a typed confirmation. After anything under
`/etc/ssh` changes, ratline reads the *effective* configuration with `sshd -T`,
confirms login still works, and rolls back if it does not.

The lockout recovery runbook is in [security/ssh-keys.md](../security/ssh-keys.md).
