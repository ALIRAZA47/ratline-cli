---
name: ratline-server-setup
description: Take a bare Ubuntu or Debian VPS to a working ratline server — the checksummed install script and its environment variables, `ratline init`, managed Node/Bun/Python runtimes with PM2, the ACME account, MongoDB on the host or an attached one, the first admin SSH key, and optionally the web panel — then prove it with `doctor`. Use when someone has a fresh server or droplet, says "set up ratline", "install ratline on my VPS", wants a runtime installed, or needs a box prepared before its first site. Also use when `ratline` is missing on a server a deploy was about to target.
---

# Setting up a ratline server

A fresh Ubuntu or Debian box, root access, and the goal is a server that `ratline status`
describes and `ratline doctor` finds nothing wrong with. Everything after the install
script is a ratline command, and every ratline command is safe to run twice, so re-running
this whole procedure on a half-set-up server converges rather than breaking anything.

Rules shared with every skill in this plugin: look flags up in `ratline schema` (or
`--help`) rather than remembering them; rehearse mutations with `--dry-run`; pipe secrets on
stdin; read the `--json` envelope and branch on the exit code (5 wait and retry, 6 stop and
get a human, 10 pass the flag). Run commands on the server as root, or from a laptop as
`ssh -T "$RATLINE_HOST" sudo ratline …` with `RATLINE_HOST` the SSH destination.

## Before touching the server

Ask, or confirm from context:

1. Root or sudo access, and the SSH key that already gets in. You will not change how root
   logs in; ratline never touches `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers`
   or `Port` without an explicit flag and a typed confirmation, and neither do you.
2. The operator's public key, to hold a **global-scope** key (server administration).
3. Contact email for certificates, and whether they accept Let's Encrypt's subscriber terms.
4. Which runtimes: Node (which major), Python (which minor), Bun.
5. A database on this host, or one elsewhere, or none yet.
6. The web panel: yes or no. It can come later without cost.

Check the OS while you are there: `. /etc/os-release && echo $PRETTY_NAME`. ratline targets
Debian and Ubuntu; anything else is a stop.

## 1. Install

```bash
curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh
```

The script resolves the latest release, downloads both binaries for the architecture,
**verifies them against the release's `SHA256SUMS`** and refuses on a missing or wrong
checksum, installs them, and runs `ratline init`. It offers to `apt-get install` nginx and
certbot if they are missing, naming them first.

Piping into a root shell deserves a look first when the operator wants one:

```bash
curl -fsSLO https://ratline.alirazakhan.me/install.sh
less install.sh && sudo sh install.sh
```

Variables the script reads, for an unattended run (cloud-init, Ansible, a CI job):

| variable | effect |
|---|---|
| `ASSUME_YES=1` | answer yes to the apt prompts |
| `RATLINE_VERSION=v0.19.0` | pin a release instead of the latest |
| `NO_INIT=1` | install the binaries and stop; run `ratline init` yourself |
| `WITH_PANEL=1 PANEL_ADMIN_EMAIL=you@example.com` | also install the web panel and create its first super admin |

```bash
curl -fsSL https://ratline.alirazakhan.me/install.sh \
  | sudo ASSUME_YES=1 WITH_PANEL=1 PANEL_ADMIN_EMAIL=ops@example.com sh
```

Afterwards: `ratline version` names the version, the OS and the runtimes it found.

## 2. init

The script ran it, but run it again with the flags you know now; it is idempotent:

```bash
ratline init --email ops@example.com --agree-tos --admin-user ops
```

`--email` and `--agree-tos` register the ACME contact so the first `cert issue` does not
stop to ask. `--admin-user` is the account that holds global-scope keys. `init` writes
`/etc/ratline/config.yaml`, the directory layout, and starts the renewal and key-pruning
timers. `--write-config-only` seeds the configuration and stops, for a box you want to
review before it does anything.

## 3. Runtimes

Managed interpreters live under `/opt/ratline/runtimes` and are invoked by absolute path
from every unit, so `nvm` and shell profiles are never involved:

```bash
ratline runtime install node 24 --with-pm2
ratline runtime install python 3.12
ratline runtime install bun 1.2
ratline runtime default node 24
ratline runtime list
```

`--with-pm2` is what gives a Node site a graceful reload; it is installed per Node version,
root-owned, deliberately. The Python install also brings the `venv` module Debian ships
separately, so `site add` does not fail three commands later.

## 4. The first admin key

```bash
ratline key add --scope global --label 'ali laptop' --key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… ali@laptop'
ratline key test 'ali laptop'
```

`--key` takes the key itself, a path, an `https://` URL, or `-` for stdin; `--from-github
<user>` fetches and asks for confirmation per fingerprint. Whatever arrives is re-rendered:
any `command=` or `no-pty` it carried is stripped. ratline verifies sshd still accepts logins
after every change under `/etc/ssh` and rolls back if not; you do not need to.

## 5. A database, if wanted now

Two different situations, two commands; the `ratline-databases` skill has the detail.

```bash
# fresh VPS, no MongoDB anywhere: install one, secured and bound to localhost
ratline db install --dry-run
ratline db install            # prompts for the admin password; or --stdin < file

# a MongoDB that already exists (Atlas, another host): attach it
ratline db connect            # prompts for the admin connection string; or --stdin / --from-file
ratline db ping
```

`db install` is the one place ratline installs a server package (`--engine mysql` or
`--engine redis` for those instead of MongoDB), and it refuses a database server it did not
set up. Nothing else ever installs a database because you asked for something adjacent.

## 6. Firewall, and what ratline will not do about it

ratline never runs `ufw enable` or `ufw disable`. Done in the wrong order that locks the
operator out of SSH, and only they know what else must stay reachable. If they want a
firewall, they do it, in this order, from a session they can afford to lose:

```bash
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw default deny incoming
ufw enable
```

Say this to them; do not run it for them unless they ask by name. Later, `ratline db access
allow <address>` adds per-address rules for the database port and requires an active
default-deny firewall to exist first.

## 7. Prove it

```bash
ratline doctor
ratline status
ratline cert account show
ratline runtime list
```

`doctor` should report nothing. `status` should show zero tenants and zero sites, the
timers armed, and no findings. Anything else, fix now while nothing depends on it.

## 8. The web panel, if wanted

If `WITH_PANEL=1` was not used:

```bash
curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
```

It asks for the first super admin's address and prints a generated password once. The
`ratline-panel` skill covers putting it on a domain and inviting people.

## Handing over

State exactly what is on the server now: version, runtimes, whether a database is attached,
which key holds global scope, whether the panel is installed, and what the operator still
has to do themselves (the firewall, DNS for future sites). Then the next step is the
`ratline-deploy` skill for the first site.
