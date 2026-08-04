# SSH keys

Keys are a managed resource with three scopes. Every key carries a required human
label, because in two years' time the difference between "Ali MacBook" and "CI
runner" is the difference between knowing what to revoke and guessing.

## Which scope?

| You are giving access to | Scope | They get |
|---|---|---|
| Yourself or your ops team | `global` | A shell as the administrator, and permission to run `ratline` |
| A client who owns several sites | `user` | A shell, and every site that user owns |
| A contractor or CI job on one site | `site` | sftp, rsync and git inside one directory. No shell |

```bash
# you
ratline key add --scope global --label "Ali MacBook" --key ~/.ssh/id_ed25519.pub

# a client
ratline key add --scope user --user acme --label "Acme ops" --from-github acme-ops

# a contractor: one site, one network, ninety days
ratline key add --scope site --site example.com --label "Contractor" \
    --key contractor.pub --from 203.0.113.0/24 --expires 90d
```

Then check what you actually granted:

```bash
ratline key test SHA256:x9K…
```

```
Key       SHA256:x9K…   "Contractor"   ed25519
Scope     site → example.com  (owner: acme)
Login     acme@server — forced command only, no interactive shell
Allowed   sftp, rsync, git-upload-pack, git-receive-pack
          confined to /home/acme/example.com (symlinks resolved)
Denied    shell, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2026-11-02 (90 days)
Last use  never observed
Note      Runs as UID acme. Not a kernel boundary — see SECURITY.md.
```

That last line is not boilerplate. Read the next section.

## What site scope does and does not enforce

A site-scoped key authenticates **as the site owner's UID**. What confines it is
sshd's forced command plus the `ratline-shell` wrapper, which:

- refuses an interactive login outright, with a message explaining what the key
  *can* do
- dispatches sftp to `internal-sftp` rooted at the site directory
- accepts `rsync --server`, `git-upload-pack`, `git-receive-pack` and `scp`, and
  nothing else
- refuses `--rsh`, `--daemon`, `--remote-option`, `--upload-pack` and every other
  flag that would turn an allowed program into an arbitrary one
- resolves every path argument, following symlinks, and refuses anything outside
  the site directory
- writes every invocation to the audit log with the key id, the remote address and
  the requested command

This reliably stops accidents. A contractor's rsync cannot reach a sibling site; a
misconfigured CI job cannot overwrite the wrong directory.

It does **not** stop someone who already has code execution as that UID. If they
can run arbitrary code — through the application, a dependency, or a bug in the
wrapper — they have the tenant's UID and everything it can reach.

**For genuine isolation, give the site its own user:**

```bash
ratline user add example-com
ratline site add example.com --user example-com --runtime static
ratline key add --scope user --user example-com --label "Contractor" --key contractor.pub
```

Now the boundary is the kernel's, not a wrapper's. `ratline user add` is cheap on
purpose.

## Every key starts from `restrict`

Whatever the scope, the options begin with OpenSSH's `restrict`, which turns off
port forwarding, agent forwarding, X11, PTY allocation and the user rc file. What a
scope needs is then re-enabled explicitly.

This direction matters: when a future OpenSSH adds a capability that `restrict`
covers, a ratline key is safe by default rather than newly exposed.

## A key from a new laptop

```bash
# from the new machine
ssh-keygen -t ed25519 -C "ali@thinkpad"

# on the server, from a machine you can still reach
ratline key add --scope global --label "Ali ThinkPad" --key ~/.ssh/id_ed25519.pub
```

Or pull it from GitHub, which avoids copying a blob between terminals:

```bash
ratline key add --scope global --label "Ali ThinkPad" --from-github alikhan
```

Fetched keys are shown as fingerprints and confirmed before anything is written.
Every line is validated independently, over HTTPS with full certificate
verification, and a redirect to a different host is refused.

**Test the new key before you close the old session.** This is the whole
discipline: open a second terminal, log in with the new key, and only then walk
away.

## CI deploy keys, in both directions

These are two different things and it is worth being precise about which you need.

**Inbound — CI pushes to your server.** A site-scoped key, so a compromised CI
runner reaches one directory:

```bash
ratline key add --scope site --site example.com --label "GitHub Actions" \
    --key ci.pub --from 140.82.0.0/16 --expires 180d
```

**Outbound — your server pulls from a private repository.** ratline generates the
keypair and prints the public half to paste into the repository's deploy keys:

```bash
ratline site deploy-key create example.com
```

The private half is `0600`, owned by the site user, and never leaves the box.

## Rotating and revoking

```bash
# add the replacement first, confirm it works, then remove the old one
ratline key add --scope global --label "Ali MacBook 2027" --key new.pub
ratline key remove SHA256:old…

# a compromised key: take it off every scope and add it to the revoked list,
# which sshd consults regardless of any authorized_keys file
ratline key revoke SHA256:leaked…

# what has gone stale?
ratline key list --unused 90
ratline key audit
```

`key audit` reports duplicates across scopes, weak algorithms, keys never observed
in use, expired keys still installed, keys added outside ratline, permission
problems that make sshd silently ignore a file, and labels that promise less access
than the key grants.

Last-used data comes from scraping accepted-publickey lines out of the journal, on
a daily timer. That is on purpose: logs rotate, so a contractor's key last used four
months ago leaves no trace by the time anyone asks — recording it as it happens is
what makes `--unused 90` mean something.

## Expiry

```bash
ratline key add … --expires 90d
ratline key add … --expires 2026-12-31
```

On OpenSSH 8.2 and later this becomes an `expiry-time=` option and sshd itself
refuses the key. ratline detects the version; on anything older it says so at add
time rather than silently ignoring the flag, and the daily timer removes the key
when it expires either way.

## Keys you added by hand

ratline writes only between its markers:

```
# >>> ratline managed — do not edit by hand; use `ratline key add|remove`
# ratline id=k_7f3a label="Contractor" scope=site site=example.com added=2026-08-04 by=ali
restrict,expiry-time="20261102000000",from="203.0.113.0/24",command="/usr/local/lib/ratline/ratline-shell --site example.com" ssh-ed25519 AAAA… contractor@laptop
# <<< ratline managed
```

Anything outside those markers is preserved byte for byte through every
`key sync`, `key add` and `reconcile`. `key audit` reports it as unmanaged so you
know it is there, and nothing removes it automatically — deleting something you put
there deliberately would be worse than leaving it.

If you paste a key that carries its own options, they are **stripped**, not
honoured. A `command=` or `permitopen=` arriving from an untrusted source is an
escalation vector; only the options ratline derived from your flags are written, and
you are told what was discarded.

## I am locked out

Assume you have console access through your provider's web UI and nothing else.

**1. Get a root shell.** Most providers offer a serial or VNC console. Failing
that, boot a rescue image and chroot into the disk.

**2. Find out what sshd thinks.**

```bash
sshd -t                          # does the configuration parse?
sshd -T | grep -iE 'pubkey|authorizedkeys|permitroot|port'
systemctl status ssh
journalctl -u ssh -n 50
```

**3. Restore a backup.** ratline backs up every file it touches under `/etc/ssh`
before changing it:

```bash
ls -t /etc/ssh/sshd_config.d/60-ratline.conf.ratline-backup-*
cp /etc/ssh/sshd_config.d/60-ratline.conf.ratline-backup-<newest> \
   /etc/ssh/sshd_config.d/60-ratline.conf
sshd -t && systemctl reload ssh
```

**4. Or remove ratline's drop-in entirely.** It only adds `RevokedKeys` and
`Match` blocks for SFTP-only users; deleting it cannot break a normal login:

```bash
rm /etc/ssh/sshd_config.d/60-ratline.conf
sshd -t && systemctl reload ssh
```

**5. Add a key back.**

```bash
mkdir -p /root/.ssh && chmod 700 /root/.ssh
echo 'ssh-ed25519 AAAA… you@laptop' >> /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
```

Then, once you are back in over SSH, put it under management:

```bash
ratline key add --scope global --label "Recovery key" --key /root/.ssh/authorized_keys
ratline key sync
ratline doctor
```

**6. The most common cause is permissions.** sshd silently ignores a key file that
is too open, and logs almost nothing about it:

```bash
chmod 750 /home/acme
chmod 700 /home/acme/.ssh
chmod 600 /home/acme/.ssh/authorized_keys
```

`ratline doctor` checks all three and reports them as problems, which is usually
faster than reading the auth log.
