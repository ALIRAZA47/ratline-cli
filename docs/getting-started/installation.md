# Installation

Requires Ubuntu 22.04+ or Debian 12+, root, and nginx. Other distributions may
work; ratline warns rather than refuses, because the filesystem layout it assumes
is Debian's.

## One command

```bash
curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh
```

That resolves the latest release, downloads the binaries for this architecture, verifies
them against the release's own `SHA256SUMS`, installs `ratline` and the `ratline-shell`
wrapper, and runs `ratline init` — which writes the configuration, creates the directory
layout, and installs and starts the renewal and key-pruning timers. It also offers to
`apt-get install` nginx and certbot if they are missing, naming them first.

Piping a script into a root shell is a real supply-chain risk, and worth being deliberate
about. The script checksums everything it downloads, and refuses rather than warning if a
checksum is missing or wrong — but that cannot protect you from a compromised release. If
you would rather read it first, which is reasonable, it is the same two commands:

```bash
curl -fsSLO https://ratline.alirazakhan.me/install.sh
less install.sh && sudo sh install.sh
```

Useful variables:

| | |
|---|---|
| `RATLINE_VERSION=v0.4.0` | Install a specific release rather than the latest |
| `ASSUME_YES=1` | Answer every prompt yes — for Ansible, cloud-init or a Dockerfile |
| `NO_INIT=1` | Install the binaries and stop, leaving `ratline init` to you |
| `PREFIX=/opt/ratline` | Install somewhere other than `/usr/local` |

## From a release, by hand

```bash
curl -fsSLO https://github.com/ALIRAZA47/ratline-cli/releases/latest/download/ratline-v0.4.0-linux-amd64.tar.gz
tar -xzf ratline-v0.4.0-linux-amd64.tar.gz
cd ratline-v0.4.0-linux-amd64
sudo sh install.sh
```

The tarball carries both binaries, the installer and this release's checksums; the
installer uses what is beside it rather than downloading anything.

## From a .deb

```bash
sudo dpkg -i ratline_0.4.0_amd64.deb
```

Built with `make deb`, which needs [nfpm](https://github.com/goreleaser/nfpm). The
package's postinstall runs `ratline init --write-config-only`, so the configuration,
directories and timers are in place when `dpkg` returns.

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

The one-command install above already ran this; it is here for the by-hand paths and
because it is worth running again after an upgrade.

`init` is interactive and idempotent. It creates the directory tree, writes
`/etc/ratline/config.yaml`, records the ACME contact address and whether the CA's terms
have been accepted, installs the nginx snippets and the systemd target, and installs and
starts the renewal and key-pruning timers. Those units come out of the binary itself
rather than from files next to an installer, which is what lets a server set up from a
single downloaded binary still renew its certificates.

It never overwrites a unit you have edited: a file at one of ratline's paths that does not
carry the `# managed-by: ratline` header is left alone, and reported.

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
