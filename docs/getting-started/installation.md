# Installation

Requires Ubuntu 22.04+ or Debian 12+, root, and nginx. Other distributions may
work; ratline warns rather than refuses, because the filesystem layout it assumes
is Debian's.

## From a release

```bash
curl -fsSLO https://github.com/ALIRAZA47/ratline-cli/releases/latest/download/ratline-linux-amd64
curl -fsSLO https://github.com/ALIRAZA47/ratline-cli/releases/latest/download/install.sh
sudo sh install.sh
```

`install.sh` verifies the checksum, installs to `/usr/local/bin/ratline`, installs
the `ratline-shell` wrapper, the systemd timers for renewal and key pruning, and
the shell completions.

## From a .deb

```bash
sudo dpkg -i ratline_1.0.0_amd64.deb
```

## From source

```bash
git clone https://github.com/ALIRAZA47/ratline-cli && cd ratline-cli
make build && sudo make install
```

The binary is static — `CGO_ENABLED=0`, with a pure-Go SQLite driver — so there is
nothing to install alongside it.

## Initialise the server

```bash
sudo ratline init
```

`init` is interactive and idempotent. It creates the directory tree, writes
`/etc/ratline/config.yaml`, detects the server's public addresses, records the ACME
contact address and whether the CA's terms have been accepted, and installs the
nginx snippets and the systemd target. Run it again after an upgrade; it changes
only what is missing.

## Check the result

```bash
sudo ratline doctor
```

Exit code 0 means the server is ready. Anything it reports comes with the command
that fixes it.

```bash
sudo ratline status
```

The inventory: what is on this server and what state it is in. On a fresh install
that is nothing, and `status` says so along with the two commands that change it.

## Install a runtime

Only needed for node and python sites.

```bash
sudo ratline runtime install node 22 --with-pm2
sudo ratline runtime install python 3.12
```

`--with-pm2` installs the process manager a node site is supervised by. Without it
a node site has to be created with `--daemon direct`, which works but cannot reload
without dropping requests — see [node-sites.md](../guides/node-sites.md).

Managed interpreters live under `/opt/ratline/runtimes`, are root-owned, and are
invoked by absolute path from the generated units. nvm and shell profiles are never
involved, because systemd does not read them: a unit that depended on one would
work when tested by hand and fail on the next boot.

## Shell completion

```bash
ratline completion bash | sudo tee /etc/bash_completion.d/ratline
ratline completion zsh  | sudo tee /usr/share/zsh/site-functions/_ratline
```

Completion is dynamic: it offers the domains, tenants, certificates and key
fingerprints that exist on *this* server. It needs no privileges and returns
nothing rather than an error when it cannot read state.

Next: [first-site.md](first-site.md).
