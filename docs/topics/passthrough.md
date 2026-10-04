# nginx, systemctl and journalctl through ratline

> The system tools an operator reaches for, with ratline supplying the binary, the unit
> name and the journal namespace, and refusing what would fight it.

    ratline nginx -- -t
    ratline nginx -- reload
    ratline systemctl -- status app.example.com
    ratline systemctl -- restart app.example.com
    ratline journalctl app.example.com -- -n 200 -f

Nothing stops you running the real tools as root, and these commands do not pretend
otherwise. What they add is the part you would otherwise have to carry in your head:
which unit a site runs as, which journal its logs are in, and which operations leave
ratline's picture of the server wrong.

Everything after `--` is an argv for the tool. It is never handed to a shell, and the
tool is found where ratline finds every binary, never on `PATH`. The tool's own exit code
is reported rather than adopted: `systemctl status` of a stopped unit exits 3, and
ratline exits 4 (an external command failed) naming the 3. Under `--json` the tool's
output goes into the envelope as `stdout`, `stderr` and `exit_code`.

The `--` matters. Before it, `-v` and `-q` are ratline's own `--verbose` and `--quiet`,
and anything else is a flag ratline does not have.

## nginx

The switches that only read pass straight through, each as its own argument: `-t`
(test the configuration), `-T` (test it and print everything nginx loaded), `-v`, `-V`
and `-q`. A cluster such as `-tq` is refused with the separated spelling, and `-q` on its
own is refused because nginx without `-t`, `-T`, `-v` or `-V` starts a second master.

`reload` (or `-s reload`) is not passed to nginx. It becomes what ratline itself does
after changing a vhost: `nginx -t`, and only if that passes, `systemctl reload nginx`,
waiting until no worker started under the old configuration is still accepting. Two
things are wrong with `nginx -s reload`: it signals the master behind systemd's back, and
it reloads whatever is on disk whether it tests clean or not.

Refused, each with the reason: `-s stop` and `-s quit` (ratline would have no web server,
and every site would be down with nothing in state saying so), `-s reopen` (logrotate
already does it), and `-c`, `-g`, `-p` and `-e`, which point nginx at a configuration
other than the one ratline manages.

## systemctl

A site's domain (or alias) is accepted wherever a unit name is, and becomes the site's
unit. `--no-pager` is always passed.

    ratline systemctl -- status app.example.com
    ratline systemctl -- list-units 'ratline-*' --all
    ratline systemctl -- cat app.example.com

**Reads** work on any unit or pattern: `status`, `show`, `cat`, `is-active`,
`is-enabled`, `is-failed`, `list-units`, `list-timers`, `list-unit-files`, with `--all`,
`--full`, `--failed`, `--plain`, `--no-legend`, `--value`, `--quiet`, `--recursive`, and
`--state=`, `--type=`, `--property=`, `--lines=` and `--output=` written with their value
in the same argument.

**Control** — `start`, `stop`, `restart`, `reload`, `try-restart`, `reload-or-restart`,
`reset-failed` — works only on the units ratline manages and on nginx:

- `ratline-*.service` and `ratline-*.timer` (sites, workers, jobs, `ratline-pm2@<user>`,
  ratline's own timers),
- `ratline.target`,
- `systemd-journald@<slug>.service`, a site's journal namespace, when the slug is a site's,
- `nginx` / `nginx.service`, whose configuration is tested before a start, restart or
  reload.

Each unit is named; no patterns and no switches. Anything else on the machine is yours to
run with `systemctl` directly.

`ratline site restart` does more than `ratline systemctl -- restart` for a site: it makes
sure the socket directory exists and health-checks the result. Use it after a change; use
this when you want exactly what systemctl does.

**Refused**, each pointing at what does the job properly:

| Verb | Instead |
|---|---|
| `enable`, `disable` | `ratline site enable` / `ratline site disable`, which keep nginx, the unit and state in step |
| `mask`, `unmask`, `freeze` | `ratline site disable` / `ratline site stop` |
| `edit`, `set-property`, `revert` | `ratline site scale` for limits; `ratline reconcile --fix` undoes a hand edit |
| `daemon-reload`, `link`, `preset` | ratline reloads systemd itself; `ratline reconcile --fix` repairs drift |
| `kill` | `restart`, or `ratline site restart` |
| `set-environment` and friends | `ratline site env set <domain> --stdin` |
| `isolate`, `reboot`, `poweroff`, … | the whole machine; ratline will not do that for you |

## journalctl

Every unit ratline runs for a site logs into the site's own journal namespace. That is
what lets a tenant read their own site's logs and nobody else's (see `ratline explain
layout`), and it has a cost: `journalctl -u ratline-acme-app_example_com.service` reads
the shared journal, finds nothing, and prints `-- No entries --` at exactly the moment
somebody is debugging.

    ratline journalctl app.example.com
    ratline journalctl app.example.com -- -n 200 -p warning
    ratline journalctl app.example.com -- -f
    ratline journalctl nginx.service -- --since '10 min ago'

Given a site, this reads the site's service with `--namespace=+<slug>`, which merges the
namespace with the shared journal, so lines from before the site moved into its
namespace still show. Given a unit, it reads that unit, from its namespace if the unit
file names one — so a site's worker unit, named directly, works too. A static site has no
unit; its logs are nginx's, in `ratline logs <domain> --access`. A name that looks like a
domain and is not a site is refused rather than read as an empty unit.

`-f` follows until Ctrl-C, with no timeout, and cannot be combined with `--json`.

Refused, because they write, read a different journal, or replace the scope ratline set:
`--vacuum-size`, `--vacuum-time`, `--vacuum-files`, `--rotate`, `--flush`, `--sync`,
`--relinquish-var`, `--smart-relinquish-var`, `--setup-keys`, `--update-catalog`,
`--cursor-file`, `-D`/`--directory`, `-i`/`--file`, `--root`, `--image`, `--namespace`,
`-M`/`--machine`, `-m`/`--merge`, `-u`/`--unit` and `--user-unit`. Both the `--flag=value`
and `--flag value` spellings are caught, and so is an abbreviation (`--rot`), because
journalctl would accept one.

A tenant without root reads their own site's journal with
`ratline logs <domain> --journal`, which reads their namespace alone.

## Locking

`ratline nginx -- reload` and a `systemctl` control verb take the server lock, so neither
lands in the middle of a `site add` or a deploy. Reads never do — `systemctl status`
answers during a twenty-minute deploy rather than waiting for it, or failing with exit 5
because one is running. Because one command covers both, the decision is made once the
arguments have been read rather than from the command's name.

Under `--dry-run` a control verb prints what it would run and runs nothing; a reload runs
only the read-only `nginx -t`, so the rehearsal does not promise a reload nginx would
refuse. Reads run as they always do.

## In the panel

`nginx` and `systemctl` are a super admin's, since one form covers both a read and a
stop. `journalctl` is not offered: a site's journal is already on its logs page, and a
follow would never return.
