# Security

| | |
|---|---|
| [model.md](model.md) | What is enforced, and honestly where the isolation ends |
| [ssh-keys.md](ssh-keys.md) | The three scopes, and the lockout recovery runbook |
| [tls.md](tls.md) | Issue, attach, renew; rate limits; the orange-cloud trap |

## The short version

Each tenant is a system user. The boundary is the kernel's — uid, gid, file
permissions, cgroups, systemd namespaces — not a convention inside one process. Read
[model.md](model.md) for what that does and does not buy you, including the parts that
are genuinely not isolated.

## Things ratline refuses to do

* run as anything but root, or run from a group- or world-writable binary
* overwrite a file at one of its own paths lacking the `# managed-by: ratline` header
* grant sudo to a tenant unless configuration allows it, and then only after
  `visudo -c` validates the result
* add a key to root's `authorized_keys` unless configuration allows it *and* it is
  asked for explicitly
* build a shell command string — everything is an argv list, and there is no shell in
  the binary registry at all
* enable HSTS on a self-signed or staging certificate
* change `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` or `Port` without an
  explicit flag and a typed confirmation

The concept page, readable on the server: `ratline explain safety`.

## Reporting a vulnerability

See the bottom of [model.md](model.md).
