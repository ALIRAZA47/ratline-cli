# SSH access

> Three scopes, what each one means, and why a key is never trusted as submitted.

A key is added at one of three scopes, and the scope decides what it can reach:

    global   the admin account — server administration
    user     one tenant's account — everything that tenant owns
    site     one site, restricted to file transfer by default

    ratline key add --user acme --file ~/.ssh/id_ed25519.pub --label "deploy laptop"
    ratline key add --site app.example.com --file deploy.pub --label "ci"
    ratline key list --user acme
    ratline key remove <fingerprint>

## Keys are rewritten, not appended

Any options a submitted key line already carries are stripped and replaced. A line
that arrives with `command=` or `no-pty` of its own would otherwise silently change
what ratline thought it was granting.

The baseline is OpenSSH's `restrict`, which turns off port forwarding, agent
forwarding, X11 forwarding, PTY allocation and user-rc — and, being a single option,
picks up anything OpenSSH adds to the list later. Permissions are then added back
deliberately.

## Site scope is file transfer only

A site-scoped key gets sftp, rsync and git, through a forced command. It is for CI
and for a designer who needs to upload files, not for a shell. `--allow-shell`
overrides it per key, with a warning, because that is a different grant than the one
the scope implies.

## Managed blocks

ratline's keys live between markers in `authorized_keys`:

    # BEGIN ratline managed keys — do not edit between the markers
    ...
    # END ratline managed keys

Anything outside the markers is left exactly as it was. A tenant's own hand-added
key is never removed by a ratline operation.

## Expiry and revocation

    ratline key add --user acme --file ci.pub --expires 2026-12-31
    ratline key revoke <fingerprint>

Expiry uses OpenSSH's `expiry-time=` where the server supports it, and a daily timer
prunes expired keys whether it does or not — so an expiry is real on an older sshd
too. Revocation adds the key to a revocation list as well as removing it, so
re-adding the same key later is refused rather than quietly accepted.

## Changes to sshd are verified

After anything under `/etc/ssh` changes, ratline runs `sshd -T` to read the
*effective* configuration and confirms login still works, rolling back if it does
not. `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` and `Port` are never
touched without an explicit flag and a typed confirmation. Locking yourself out of a
remote server is the one mistake with no recovery path from the CLI.

See also: `ratline explain layout`.
