# SSH access

> Three scopes, what each one means, and why a key is never trusted as submitted.

A key is added at one of three scopes, and the scope decides what it can reach:

    global   the admin account — server administration
    user     one tenant's account — everything that tenant owns
    site     one site, restricted to file transfer by default

    ratline key add --scope user --user acme --key ~/.ssh/id_ed25519.pub --label "deploy laptop"
    ratline key add --scope site --site app.example.com --key deploy.pub --label "ci"

`--key` takes a path, an `https://` URL, `-` for stdin, or the key itself — pasting is what
everybody does at a prompt, and a public key is not a secret, so there is no reason to make
you save it to a file first:

    ratline key add --scope user --user acme --label laptop \
      --key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop'

Whichever way it arrives, it is parsed and checked identically.
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

    ratline key add --scope user --user acme --key ci.pub --expires 2026-12-31
    ratline key revoke <fingerprint>

Expiry uses OpenSSH's `expiry-time=` where the server supports it, and a daily timer
prunes expired keys whether it does or not — so an expiry is real on an older sshd
too. Revocation adds the key to a revocation list as well as removing it, so
re-adding the same key later is refused rather than quietly accepted.

The list is `RevokedKeys` in ratline's sshd drop-in, and it has one property worth
knowing about because it is the opposite of the obvious guess:

> `sshd_config(5)`: if this file is not readable, then public key authentication will
> be refused for all users.

Not the keys on the list — **every** key, for every account. So a missing revocation
list does not let revoked keys back in; it closes the server. ratline creates the list
before the drop-in refers to it, keeps an empty one on a server that has revoked
nothing, and refuses to name a file it could not create. `ratline doctor` reports the
state as a problem if it ever arises, and `ratline key sync` repairs it.

This is worth spelling out because none of the usual checks see it: `sshd -t` accepts
the directive, since the syntax is valid, and `sshd -T` prints the path without opening
it. It cost the author a live server before the verification below learned to read the
file rather than trust the configuration.

## Changes to sshd are verified

After anything under `/etc/ssh` changes, ratline runs `sshd -T` to read the
*effective* configuration and confirms login still works, rolling back if it does not.
That includes opening any file the configuration tells sshd to read — a path that
parses is not the same as a path that exists, and the difference is a server nobody
can log into. `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` and `Port` are never
touched without an explicit flag and a typed confirmation. Locking yourself out of a
remote server is the one mistake with no recovery path from the CLI.

See also: `ratline explain layout`.
