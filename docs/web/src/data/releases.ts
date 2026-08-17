/**
 * The release history.
 *
 * Written here rather than generated from git tags, because the interesting part of a
 * release is not the list of commits — it is what changed for whoever runs this, and what
 * is still missing. A generated changelog says "fix: handle nil case"; this says which
 * command stopped lying to you.
 *
 * Kept in the same order as the GitHub releases page, newest first.
 */

export interface ReleaseChange {
  /** A short label for the change, used as the heading. */
  title: string;
  /** What changed and why it mattered. Plain sentences; no bullet soup. */
  body: string;
  /** Shown as a code block under the body. */
  code?: string;
  /**
   * `fix` marks something that was wrong and is now right, which is the category people
   * scan for when deciding whether to upgrade.
   */
  kind?: 'feature' | 'fix' | 'security';
}

export interface Release {
  version: string;
  date: string;
  /** One sentence, shown under the version heading. */
  summary: string;
  /** The upgrade command, when it differs from the usual one. */
  upgrade?: string;
  changes: ReleaseChange[];
  /** Honest about what this release does not do. */
  known?: string[];
  /** Assertions in the integration suite at this tag. */
  assertions?: number;
}

export const releases: Release[] = [
  {
    version: 'v0.13.0',
    date: '2026-08-16',
    summary:
      'ratline db now provisions MySQL and MariaDB alongside MongoDB, under one --engine flag — and bun joins node and python as a site runtime.',
    assertions: 555,
    changes: [
      {
        kind: 'feature',
        title: 'MySQL and MariaDB, under `db --engine mysql`',
        body:
          'The whole `ratline db` surface — install, connect, ping, create, list, show, drop, roles, user add/list/password/grant/delete, access allow/revoke/list, dump, restore — now takes `--engine mysql`. MongoDB stays the default, so every existing invocation and script is unchanged. `db install --engine mysql` installs the distribution’s own package (mysql-server on Ubuntu, mariadb-server on Debian — no third-party repository), reaches the fresh server as root over the local socket to create the admin account whose password you choose, writes a managed drop-in that binds localhost only, and proves the credentials work over TCP before storing anything. `db create shop --engine mysql --owner acme --attach shop.example.com` creates the database, a user with a database-scoped GRANT, and writes a mysql:// DATABASE_URL into the site’s .env. The invariants are MongoDB’s: the admin password never touches argv (the client reads a 0600 defaults-file; statements go on stdin), names are validated then quoted because a SQL identifier cannot be a bound parameter, `db access` opens port 3306 firewall-first and verifies the bind against the listening socket, and `doctor` reports an exposed MySQL port.',
        code: `ratline db install --engine mysql
ratline db create shop --engine mysql --owner acme --attach shop.example.com
ratline db access allow 203.0.113.19 --engine mysql`,
      },
      {
        kind: 'feature',
        title: 'bun as a site runtime',
        body:
          'Alongside node and python, a site can now run on bun. `ratline runtime install bun 1.2` then `ratline site add app.example.com --user acme --runtime bun --entry server.ts --bun 1.2`. Bun transpiles on the way in, so a TypeScript entry point — .ts, .tsx, .jsx, .mts — starts directly, without a build step or a build-output directory. One process supervised by systemd behind a Unix socket, referenced by absolute path like every other runtime, so a tenant upgrading their own bun cannot change the interpreter their service executes.',
        code: `ratline runtime install bun 1.2
ratline site add app.example.com --user acme --runtime bun --entry server.ts --bun 1.2`,
      },
    ],
    known: [
      'MySQL’s apt-to-server path is proven against a modeled server in unit tests, not yet in the integration suite — the same way MongoDB shipped in v0.12.0 before v0.12.1 added its integration coverage.',
    ],
  },
  {
    version: 'v0.12.2',
    date: '2026-08-16',
    summary:
      'nginx stops warning about OCSP stapling on every reload: the directive is now emitted only for certificates that actually name a responder.',
    assertions: 555,
    changes: [
      {
        kind: 'fix',
        title: 'ssl_stapling warned on every reload for Let’s Encrypt certificates',
        body:
          'Generated vhosts stapled unconditionally — ssl_stapling on / ssl_stapling_verify on lived in the shared TLS snippet that every vhost includes. Let’s Encrypt stopped advertising an OCSP responder in its certificates in 2026 (the AIA carries only CA Issuers, no OCSP URI), so nginx logged “ssl_stapling ignored, no OCSP responder URL in the certificate …” once per site on every `nginx -t` and every reload. Nothing broke — the handshake just carried no stapled response — but it was noise on the one command standing between an operator and a reload that takes every site on the box down, and it fired for every LE certificate issued from then on. Stapling now comes from the vhost, emitted only when the attached certificate actually names a responder, read off its AIA extension at generation time. It is deliberately not keyed on source: an imported or private-CA certificate that still names a responder keeps stapling, and the decision follows the certificate as CA policy changes rather than being frozen at issue time. For existing sites the directive lived in the shared TLS snippet, so `ratline reconcile --fix` rewrites that snippet and re-renders the vhosts — deployed sites pick this up without being re-added (a plain update swaps the binary but does not rewrite the snippet). A renewal re-renders with the fresh certificate too, so a certificate renewed into a different profile flips the directive rather than keeping the issue-time choice.',
        code: `# before — once per site, on every reload:
# [warn] "ssl_stapling" ignored, no OCSP responder URL in the certificate ...`,
      },
    ],
  },
  {
    version: 'v0.12.1',
    date: '2026-08-10',
    summary:
      'The integration suite now installs MongoDB for real and steers its port; writing that test found two fixes worth having.',
    assertions: 555,
    changes: [
      {
        kind: 'fix',
        title: 'db access verified its restarts against whatever was attached',
        body:
          'Opening or closing the bind restarts mongod and then proves the outcome against a running server. That verification read the attached connection string — usually this host, but nothing forces that: an operator can re-attach ratline to Atlas while the local mongod keeps serving, and the ping would then “verify” a restart against a server the restart never touched. It also meant an unattached host could not open or close its own port at all. The verification now uses the local server’s plain URI: no credentials needed, and it proves the two facts the command promises — the server answers, and it still enforces authorization. Found by the integration harness, whose attached MongoDB really is a different server from the one db install puts on the host.',
      },
      {
        kind: 'fix',
        title: 'The bare doctor sweep hid an exposed port when provisioning was off',
        body:
          'v0.12.0 said both doctor surfaces report a mongod reachable beyond localhost with no firewall standing guard. The subject walk did; the bare sweep ran the check only when features.db_provisioning was on, so `db disable` — or never enabling provisioning — made the one finding that matters to every host invisible in the sweep. The exposure check now runs unconditionally on both surfaces: it is decided by the socket and the firewall, needs no credentials, and matters whether or not this host provisions databases.',
      },
      {
        title: 'The apt-to-mongod path is now proven, not modeled',
        body:
          'v0.12.0 shipped with a known gap: db install’s flows were checked against a modeled host. The integration suite now runs the real thing inside its Ubuntu container — the repository and pinned key, the packages, the admin user, the managed config — and asserts every promise from outside: the service running, the credentials opening it, unauthenticated commands refused, a socket bound to localhost and nowhere else, an existing attachment left alone, and a mongod ratline did not set up refused rather than adopted. The db access round trip runs against a real ufw: allow opens the bind only behind the firewall, revoke closes it again, and both halves of the doctor check fire — quiet while ufw stands guard, loud the moment it is disabled.',
      },
    ],
  },
  {
    version: 'v0.12.0',
    date: '2026-08-10',
    summary:
      'On a host with no MongoDB, one command now installs it, secures it, and attaches it — and its port opens only to addresses you allow, firewall first.',
    assertions: 522,
    changes: [
      {
        kind: 'feature',
        title: 'ratline db install — MongoDB on a fresh host, secured before it is useful',
        body:
          'ratline has never installed software, and still installs none as a side effect — but on a fresh VPS with no MongoDB anywhere, “point ratline at a server” is not actionable advice, and the manual path is long enough that people skip the one step that matters. So one explicit command now does the whole first day: MongoDB’s official apt repository (its signing key ships inside the ratline binary and is pinned with signed-by — nothing about the root of trust is downloaded), the packages, a root-role admin user with a password you choose at the prompt or via --stdin — never argv — and a managed mongod.conf that enables authorization and binds localhost only. There is no code path that renders that file without authorization enabled. The outcome is proven against the running server, not the config: it must enforce authorization and accept your credentials before the connection string is stored and provisioning turned on. If ratline is already attached to a MongoDB, the stored string is left alone. A failure unwinds everything except the downloaded packages, which stay inert — stopped and disabled — so a re-run continues. A mongod ratline did not set up is refused, not adopted.',
        code: `ratline db install
Choose a password for the MongoDB admin user (not echoed): `,
      },
      {
        kind: 'feature',
        title: 'ratline db access — the port opens only to addresses you allow',
        body:
          'Reachability is two facts that must agree — what mongod binds and what the firewall admits — and an operator changing one by hand gets either a server that is unreachable for no visible reason or one the whole internet can brute-force, and both look identical from the machine itself. `db access allow` owns both together, in the only safe order: the ufw rule first, and only then, on the first allowed address, the wider bind and its restart. Revoking the last address puts mongod back on localhost. Three refusals guard the door, each with its own fix: no ufw, ufw inactive, or a default incoming policy of allow — and ratline never runs `ufw enable` itself, because done in the wrong order it locks you out of SSH and only you know what else must stay reachable. Every transition is verified against the running server: still enforcing authorization, and actually bound where the config says — the bind check reads the listening socket via ss, because the first implementation trusted the config file and a test proved a restart that silently ignores it would have reported success.',
        code: `ratline db access allow 203.0.113.19
ratline db access allow 10.8.0.0/24 --note vpn
ratline db access list
ratline db access revoke 203.0.113.19`,
      },
      {
        kind: 'feature',
        title: 'doctor sees an exposed mongod, in the sweep and the walk',
        body:
          'A mongod listening on every interface behind a ufw somebody later disabled is a misconfiguration no command refuses, because it happens outside ratline. Both doctor surfaces now report it — answered from the listening socket and the firewall’s own status, never from what a config file says. One shared implementation behind both, because a check added to the walk and not the sweep is a mistake this project has already made twice and written down.',
      },
    ],
    known: [
      'The integration suite does not yet exercise the real apt-to-mongod path; db install’s flows are proven against a modeled host in unit tests.',
      'db access manages the mongod that db install set up. For Atlas or another host, the access list lives with that server, and the commands say so rather than pretending a local firewall rule would help.',
    ],
  },
  {
    version: 'v0.11.5',
    date: '2026-08-09',
    summary:
      'A security review closed three ways a value could become a directive, or a root write could be redirected, that the tool trusted it would not.',
    assertions: 522,
    changes: [
      {
        kind: 'fix',
        title: 'A manifest could inject nginx and systemd directives',
        body:
          '`restore` reads a site manifest — a file that lives in the tenant’s own directory and may have been edited or come from somewhere untrusted — and renders straight from it. It validated the domain, the owner and the aliases, on the argument that those reach a config, and trusted the rest. But the index file, the document root, the static location, the commands and the limits reach a config too: a value like `index.html; root /etc` becomes a real nginx directive that `nginx -t` accepts, because it is syntactically valid. Confirmed by rendering it — a poisoned index_file adds a `location` block serving /etc. Every render-bound field is now validated as if it had been typed, from one gate shared by `restore`, `site add`, `import` and `clone`.',
        code: `# a manifest field, before:
index_file: "index.html; root /etc"
#   → index index.html; root /etc;   (a directive nginx accepts)`,
      },
      {
        kind: 'fix',
        title: 'A scheduled job’s command or timeout could inject systemd directives',
        body:
          '`site cron`/`site worker` checked the command for shell metacharacters but not for a newline, and did not check the timeout at all — and both are written verbatim into a systemd unit. A newline ends the `ExecStart` line and starts a directive of the operator’s choosing, which `systemd-analyze verify` accepts because a second `ExecStart` is valid. Reachable through a crafted `export` handed to `ratline import`, and through `site clone`. Control characters are refused now, at the render boundary as well as the CLI, and the timeout must parse as a duration.',
      },
      {
        kind: 'fix',
        title: 'A root write could be redirected through a tenant’s symlink',
        body:
          'ratline writes a site’s .env, logs and manifest as root into a directory the tenant owns. A tenant with shell access could replace one of those directories with a link to, say, /etc/cron.d between operations; the next write used `os.Stat`, which follows the link, and would land as root wherever it pointed. The writes now `Lstat` and refuse a symlinked directory, and site provisioning walks the whole path from the root-owned /home boundary so a link swapped higher up is caught too.',
      },
      {
        title: 'What the review found solid',
        body:
          'No command is ever built as a shell string; SQL is parameterised throughout; environment values reject newlines; a repository URL cannot start like a git flag; restore’s archive extraction already refuses absolute and traversing paths; secrets are 0600, kept off argv, and never logged. Those held.',
      },
    ],
  },
  {
    version: 'v0.11.4',
    date: '2026-08-09',
    summary:
      'A revocation list sshd was told to read and could not refused every key on the server. Update if you use ratline to manage SSH keys.',
    assertions: 522,
    changes: [
      {
        kind: 'fix',
        title: 'RevokedKeys naming a missing file locked the server',
        body:
          '`sshd_config(5)`: “if this file is not readable, then public key authentication will be refused for all users.” Not the keys on the list — every key, for every account. So a missing revocation list does not let revoked keys back in; it closes the server. ratline wrote the drop-in naming that file without ensuring it existed, so on a host where the list had never been created the first `key add` shut the door. This was found the hard way: it locked the author out of the test server, recoverable only from the provider’s console.',
        code: `RevokedKeys /etc/ratline/ssh/revoked_keys   # and the file was not there`,
      },
      {
        title: 'Three guards were in place and none of them caught it',
        body:
          '`sshd -t` accepts the directive, because the syntax is valid. `sshd -T` reports the path without ever opening it — so the post-change verification, which reads the *effective* configuration precisely so it cannot be fooled by Include and Match, was fooled by a filename. And the comments in the diagnostics asserted the opposite of the truth in two places: “sshd tolerates a missing RevokedKeys file, which is precisely the problem: a revoked key silently works again.” Both halves wrong, and that inverted belief is why the code was written this way.',
      },
      {
        title: 'Fixed in layers, because one guard already proved insufficient',
        body:
          'The list is created before the drop-in names it — empty on a server that has revoked nothing, which refuses nothing and is the correct content. If it cannot be created the directive is omitted entirely: a server without its revocation backstop is weaker, a server naming a file sshd cannot read is gone. And the verification now opens the file rather than trusting the path, which is the layer that matters because it catches the state however it arises, including an operator deleting the list later. Reverting the first fix makes the suite fail with “the sshd change was reverted” — even with the bug back, the server stays reachable.',
      },
      {
        title: 'doctor reports it, in the sweep and not only in `doctor ssh`',
        body:
          'Adding it to the walk alone was the first attempt, and the walk is not what a cron job runs — the same mistake as two releases ago, now written down as a rule.',
      },
    ],
    known: [
      'If you are already locked out by this: from a console, `rm /etc/ssh/sshd_config.d/60-ratline.conf && systemctl reload ssh`, then update and run `ratline key sync`.',
    ],
  },
  {
    version: 'v0.11.3',
    date: '2026-08-08',
    summary:
      'A job that failed once and was then deleted kept reporting itself as failed to `systemctl --failed` for ever.',
    assertions: 515,
    changes: [
      {
        kind: 'fix',
        title: 'A removed job left its failed state behind in systemd',
        body:
          'Removing a job or worker took the unit files away and left systemd remembering that the unit had failed. The entry becomes “not-found failed” and stays in `systemctl list-units --all` and, worse, in `systemctl --failed` — which is what an operator looks at and what a monitoring check watches. A job that failed once and was then deleted would alarm about itself indefinitely, for a unit that no longer exists and no file on disk mentions. `reset-failed` runs after the reload, not before.',
        code: `● ratline-acme-app_example_com-job-nightly.service  not-found  failed  failed`,
      },
      {
        title: 'How it was found',
        body:
          'By snapshotting a real server, running every feature against throwaway tenants, tearing them down, and diffing the server against the snapshot. Ten aspects matched — users, groups, unit files, vhosts, homes, certificates, /etc/ratline, logrotate, and config.yaml byte for byte. The eleventh did not, and nothing on disk explained why.',
      },
    ],
  },
  {
    version: 'v0.11.2',
    date: '2026-08-08',
    summary:
      '`ratline doctor` exited 0 on every server however broken, contradicting its own help — and the documentation site has been rebuilt to be read.',
    assertions: 513,
    changes: [
      {
        kind: 'fix',
        title: '`ratline doctor` always exited 0',
        body:
          'Its help has promised “exit code 0 means healthy, which makes it usable from cron” since it was written, and it returned success unconditionally. Anybody who had wired it into a monitor was being told everything was fine. A problem now exits 7 — the same code `site health` uses. A warning does not: paging somebody for an orphaned unit or a certificate three weeks out is how a check gets muted, after which the problems go unread too.',
      },
      {
        title: 'Turning the exit code on surfaced four things it had been hiding',
        body:
          'A site in the test harness had been broken since line 400 of a 1900-line suite, because the section that deletes /run/ratline to prove the tmpfiles rule works restarted only one of the services it had orphaned. `doctor --json` wrote two envelopes once it started failing — its own and then the error one — breaking the documented promise of exactly one object on stdout and turning a jq filter that had worked for a year into one that returned two answers. And two assertions had been asserting the wrong thing, invisibly, because exit 0 was all they could ever see.',
      },
      {
        kind: 'fix',
        title: 'doctor reports one of ratline’s own timers being absent',
        body:
          'The general form of the v0.11.0 upgrade bug. A self-updater can only fix updates it performs itself, so a check that catches the state however a server got into it is worth more than the fix to the update path. Not on a box where ratline has never run: none of them exist there, and `doctor` on a bare box is being used to look at the machine before `init`.',
      },
      {
        title: 'The documentation site has been rebuilt',
        body:
          'Three columns with a real measure, reference pages that read as reference — flags, exit codes, config settings and the JSON envelope as one row treatment with an anchor per field — dark code panels with copy buttons and tabs, grouped keyboard-first search, and both themes designed rather than one inverted. Two things were broken rather than plain: the “on this page” column never appeared on a first visit to any page, because it collected headings on a single frame and every route is lazy; and the flag table rendered twice and chose with a ResizeObserver, which is why deep links into a flag sometimes went nowhere.',
      },
    ],
    known: [
      'Topic code blocks are highlighted as shell whatever they contain: language hints would clutter files that also have to read well in a terminal under SSH.',
    ],
  },
  {
    version: 'v0.11.1',
    date: '2026-08-08',
    summary:
      'v0.11.0 shipped continuous health checks, and on any server that upgraded rather than installed fresh, nothing was continuous.',
    assertions: 508,
    changes: [
      {
        kind: 'fix',
        title: '`ratline update` did not install newly-added timers',
        body:
          'EnsureTimers was only ever called by `init`, which is run once in a server’s life. That was fine while the set of ratline’s own units never changed, and wrong the moment a release added one: the health-check commands arrived and the timer did not, so the feature whose whole point is being continuous was not running. A feature that depends on a unit cannot depend on somebody thinking to run `init` again.',
        code: `$ systemctl is-active ratline-health-check.timer
inactive`,
      },
      {
        title: 'A timer that cannot be written is a warning, not a rollback',
        body:
          'The binary is already replaced and working at that point. Failing the whole update — and unwinding a good one — because a unit file could not be written would be the wrong trade. It warns and names the fix. Safe to repeat, because EnsureTimers writes only what is missing or still carries ratline’s header and leaves a hand-edited unit alone.',
      },
      {
        kind: 'fix',
        title: 'The suite only ever exercised the fresh-install path',
        body:
          'Which is why it could not have caught this. It now removes a managed timer and checks that the installing path puts it back — the property that has to hold on an upgrade as much as on an install.',
      },
    ],
  },
  {
    version: 'v0.11.0',
    date: '2026-08-08',
    summary:
      'Continuous health checks, deploy hooks, and a one-command staging copy. Nothing on this server could previously tell you a site was returning 500 to every request.',
    assertions: 505,
    changes: [
      {
        kind: 'feature',
        title: '`ratline site health` — is it answering, not is it configured',
        body:
          'doctor could already say a service had failed or a socket was missing; that is the configuration being wrong. None of it noticed a site returning 500 to every request — the unit is active, nginx is happy, the socket connects, and every visitor gets an error page. A timer now asks each site through its own socket every five minutes and records the answer.',
        code: `$ ratline status
!  app.example.com  acme  node  running  http  FAILING — HTTP 500

$ ratline doctor
problem  health  app.example.com  not answering: HTTP 500, since 2026-08-08 12:25`,
      },
      {
        title: 'What counts as failing, and what does not',
        body:
          'A 5xx fails: that is the application saying it is broken. A 4xx does not — a site whose root legitimately answers 401 is answering correctly, and counting that as down would make this useless for anything behind authentication. Static sites are skipped, having nothing to ask; disabled sites are skipped, being meant to return 503. Reporting either every five minutes would train you to ignore the page.',
      },
      {
        title: '“Since when”, and not believing a stale answer',
        body:
          'The failure streak and its start are recorded, so a site down since Tuesday says Tuesday rather than “since the last check”. One row per site, because a row per site per interval is a disk-space problem on a box where nothing rotates it. A check older than a day is reported as stale rather than believed: a recorded “healthy” from four days ago, on a server whose timer stopped, is worse than no answer because it reads as current. `site health` exits 7 so it works directly as a monitor check — and the timer treats 7 as success, because its job is to record and doctor’s is to report.',
      },
      {
        kind: 'feature',
        title: '`ratline site hook` — two points where a deploy runs your own thing',
        body:
          'A deploy was a fixed chain, so anything site-specific had nowhere to go. The pre-deploy hook runs after the pull and before install and build — after the pull deliberately, because a hook lives in the repository and running it earlier would run the previous deploy’s version of it. A failing pre-deploy hook stops the deploy before anything restarts.',
        code: `ratline site hook set app.example.com \\
    --before …/bin/maintenance-on --after …/bin/smoke-test`,
      },
      {
        title: 'A failing post-deploy hook does not roll back',
        body:
          'It reports and exits non-zero, and leaves what is running alone. The site is already serving the new code correctly; reverting a healthy site because a notification could not reach a chat room would be a worse outcome than the failure it is reacting to. Hooks are stored on the site like its build command, so `export` carries them and `import` restores them without that being wired separately.',
      },
      {
        kind: 'feature',
        title: '`ratline site clone` — staging that is actually the same',
        body:
          'Every setting the source has, on a new domain. Standing up staging by hand means reading `site show` and retyping fifteen flags, and the whole value of staging is being the same as production — a hand-made copy differs in the one setting somebody forgot. It composes the same commands `new` and `import` do, so it cannot develop its own idea of what a site is, and `--dry-run` prints the plan.',
        code: `ratline site clone app.example.com staging.example.com --with-files --start`,
      },
      {
        title: 'Three things a clone is deliberately not faithful about',
        body:
          'Aliases are not copied: a hostname belongs to one site, and nginx resolves a clash by whichever vhost it read first — a bug that takes a day to find. Jobs and workers come across switched off, because a staging copy of a nightly job that emails customers should not fire tonight from a server nobody is watching. TLS is off, because the new domain has no certificate and DNS may not point here yet. `--with-db` creates an empty database and prints the two commands that move the data, rather than pretending to copy it.',
      },
    ],
    known: [
      'A hook and a job command are argv, not shell lines: a pipe or redirection is refused, and belongs in a script.',
      'A site’s manifest still does not carry its jobs or hooks, so `restore` cannot rebuild them — it says so. `export` and `import` do.',
    ],
  },
  {
    version: 'v0.10.1',
    date: '2026-08-07',
    summary:
      'v0.10.0 argued jobs should be units because a crontab line is invisible to doctor, then shipped them invisible to doctor — and reported as orphans you were told to delete.',
    assertions: 450,
    changes: [
      {
        kind: 'fix',
        title: '`doctor` reported every job and worker as an orphan',
        body:
          'The orphan scan walks /etc/systemd/system for ratline-*.service and reports anything that is not a site’s own unit. Job and worker units matched, so a healthy server came back with each of them flagged as "a ratline unit with no matching site" — and a suggested fix that deletes a working scheduled job. This is the same mistake the code comment beside it already documents: it once told people to delete their certificate-renewal timer. A job unit belongs to a site; it is simply not that site’s own service.',
        code: `warning  orphan  ratline-acme-app_example_com-job-nightly.service
  orphan: systemctl disable --now … && rm /etc/systemd/system/…`,
      },
      {
        title: 'And it now reports what actually goes wrong with them',
        body:
          'A job whose last run failed. A worker that keeps exiting and being restarted. A worker enabled but not running. A job whose timer is not armed — which is a job that looks configured and never runs. A worker that is merely starting is told apart from one that is crash-looping by the sub-state rather than the active state: both read as "activating", and reporting the healthy one would make the page cry wolf every time somebody adds a worker.',
      },
      {
        kind: 'fix',
        title: 'The orphan scan now covers timers',
        body:
          'It only ever looked at .service files, so a leftover .timer was invisible — and that is the residue that matters most, because it keeps firing every night to start a job for a site that no longer exists.',
      },
      {
        title: 'status, site show and MCP',
        body:
          '`status` counts scheduled jobs and workers alongside sites and certificates. `site show` lists them with their schedules, so the page that claims to show a site shows the part most likely to be quietly broken. Agents get `ratline_site_jobs`. And `restore` says plainly that an archive holds a site’s files and not its scheduled jobs, rather than leaving a restored site looking finished.',
      },
      {
        kind: 'fix',
        title: 'The test hole that let it ship',
        body:
          'The integration suite’s jobs section deleted its site before teardown, so `doctor` never once ran with a job present. It does now — and reverting the fix makes that assertion fail with exactly the output above.',
      },
    ],
    known: [
      'A site’s manifest still does not carry its jobs, so `restore` cannot rebuild them — it says so. `export` and `import` do carry them.',
    ],
  },
  {
    version: 'v0.10.0',
    date: '2026-08-07',
    summary:
      'The four things the tool promised or implied and did not have: a far end for export, somewhere to put a nightly job, somewhere to put a worker, and a backup that includes the data.',
    assertions: 441,
    changes: [
      {
        kind: 'feature',
        title: '`ratline import` gives `export` a far end',
        body:
          '`export` has said "for migration" since it was written and nothing consumed it. A dump nothing reads is a promise, not a feature: you get a file that looks like a migration and find out on the new server that there is no other half. It rebuilds the shape — tenants, keys, sites with every setting, aliases, which sites were disabled, and now the scheduled jobs — as one transaction, and is safe to run twice.',
        code: `ssh old-server ratline export | ratline import -
ratline import server.json --dry-run
ratline import server.json --only acme`,
      },
      {
        title: 'It says what it could not bring',
        body:
          'Application code, environment values, certificates and database contents are not in an export, by design. All four are listed when it finishes, because a migration that exits 0 in silence reads as done and the operator finds out at the first 502. A revoked key is not restored either: re-adding one hands back access somebody took away on purpose, mid-migration, for a key nobody is thinking about.',
      },
      {
        kind: 'feature',
        title: '`ratline site cron` and `ratline site worker`',
        body:
          'Every real application has a nightly job, and there was nowhere to put one. A crontab line runs outside every limit the site is held to — no memory ceiling, no filesystem protection, no cgroup — and nothing in status, doctor, reconcile, export or backup knows it is there. These are systemd units carrying the site’s tenant, directory, .env, sandbox and ceiling.',
        code: `ratline site cron add app.example.com nightly \\
    --schedule '0 3 * * *' --command …/bin/nightly

ratline site worker add app.example.com queue --command …/bin/worker`,
      },
      {
        title: 'Schedules are translated, then checked by systemd',
        body:
          'Cron or systemd’s own syntax; a cron expression is translated and handed to `systemd-analyze calendar` before anything is written, and the next few run times are printed — a translation you cannot see is one you cannot check. Two cases are refused rather than approximated: cron’s "day-of-month or day-of-week" rule, which no OnCalendar can express, and @reboot, which is not a schedule at all.',
        code: `0 3 * * * becomes *-*-* 03:00:00
next runs:
    Sat 2026-08-08 03:00:00 UTC`,
      },
      {
        title: 'What the unit does that a crontab line cannot',
        body:
          'A job is Type=oneshot, so a slow run backs up rather than overlapping itself. Timers carry a randomised delay, so a fleet of sites does not stampede the same database at 3am. --persistent runs a firing missed while the server was off, which cron has no equivalent for. A worker is PartOf the site, so stopping the site stops it. `site delete` removes them all.',
      },
      {
        kind: 'feature',
        title: '`ratline db dump` and `ratline db restore`',
        body:
          '`backup` archives a site’s files and nothing else, so a site with a database was backed up by two mechanisms, one of which did not exist. The connection string never reaches argv — /proc is world-readable, and an admin URI on a command line is the password for every database on the server. It goes in a 0600 config file instead, which a test now enforces against the argv the command actually builds.',
        code: `ratline db dump app_example_com
ratline db restore app_example_com-20260807T120000Z.archive.gz --drop
ratline db restore app.archive.gz --into app_staging`,
      },
      {
        kind: 'fix',
        title: 'A restored SSH key was not a key',
        body:
          'The state keeps a key split into algorithm, base64 body and comment. Passing the bare body to `key add` hands it something that is not a key, so it treated it as a path — "no such file: /AAAAC3Nz…" — and the whole import unwound. Correct behaviour from the transaction, wrong from the generator. Found by the integration round trip, not by a unit test.',
      },
      {
        kind: 'fix',
        title: 'Re-running an import failed on the keys, and --only did not scope them',
        body:
          '`key add` refuses a duplicate, rightly, so a second import failed rather than reporting. And --only scoped users and sites but not keys, so it dragged in every global key on the server and `--only nosuchtenant` planned work. A global key belongs to no tenant.',
      },
    ],
    known: [
      'A job’s --command is an argv, not a shell line: a pipe or redirection is refused, and belongs in a script.',
      'db dump and restore need mongodb-database-tools, which ships separately from mongosh.',
    ],
  },
  {
    version: 'v0.9.2',
    date: '2026-08-06',
    summary:
      'The preview counted what a failure would take back, and the count could not be right.',
    assertions: 350,
    changes: [
      {
        kind: 'fix',
        title: 'The preview overstated the rollback',
        body:
          'It ended "If any of them failed, the 3 things before it would be removed" — about a three-step plan, where the most that can ever be removed is two. How many things come back depends on which step fails, so any number printed there is wrong for every case but one. It says "everything created before it would be removed" now, and names the steps it will not take back rather than leaving the exception implicit: a certificate that was issued is not revoked, because that spends a rate limit.',
      },
      {
        title: 'Which step is not undone is now a property of the step',
        body:
          'It was going to be inferred from the label text. A step carries the reason it is kept, so the preview and the code that decides cannot disagree about it.',
      },
      {
        kind: 'fix',
        title: 'An integration test asserted the wrong thing',
        body:
          'The unwind test pinned an exit code, and which step fails depends on the environment — with the Node runtime available the site is built and the database step fails on its name, and without it the site step fails first. So the test passed or failed on whether a tarball downloaded, which says nothing about unwinding. It asserts that nothing is left behind, which must hold either way.',
      },
    ],
  },
  {
    version: 'v0.9.1',
    date: '2026-08-06',
    summary:
      'A dry run of a whole stack reported a failure for a stack that was perfectly buildable.',
    assertions: 347,
    changes: [
      {
        kind: 'fix',
        title: '`ratline new --dry-run` invented an error',
        body:
          'It ran each step with --dry-run passed down. So the tenant step correctly created nothing, and the site step was then told there is no such user — exit 3, for a stack with nothing wrong with it. A preview that invents errors is worse than no preview, because it stops people building things that would have worked.',
        code: `ratline new node app.example.com --user acme --with-db --dry-run

This would run 3 commands:

    ratline user add acme
    ratline site add app.example.com --user acme --ssl none --runtime node
    ratline db create app_example_com --owner acme --attach app.example.com

If any of them failed, everything created before it would be removed.

Nothing was written.`,
      },
      {
        title: 'It prints the plan instead',
        body:
          'Every command with its defaults filled in, in the order they would run. The steps are not executed, not even as dry runs of their own — each one preconditions on the one before it having really happened. What is checked is everything the command decides for you: the domain, the tenant name, the database name derived from the domain. What only the server knows is still open, and it says so rather than implying otherwise.',
      },
      {
        title: 'The closing summary can no longer drift',
        body:
          'The plan is decided once, up front, and both the preview and the "same thing, one command at a time" summary read that same list. Previously the summary re-derived the database name and always printed a `user add` line — including for a tenant that already existed and never had one run.',
      },
      {
        kind: 'fix',
        title: 'The composite had no integration coverage at all',
        body:
          'It shipped verified by hand, which is exactly how the dry run got out. Thirty assertions now cover it in the real harness: the preview writes nothing, the happy path builds and is safe to run twice, a failing step takes back the tenant and the site it created, and a tenant that already existed survives.',
      },
    ],
  },
  {
    version: 'v0.9.0',
    date: '2026-08-06',
    summary:
      'One command builds a whole stack — tenant, site, database, certificate — and removes all of it if any step fails.',
    assertions: 315,
    changes: [
      {
        kind: 'feature',
        title: '`ratline new node|python|static` provisions a stack',
        body:
          'A tenant, a site, optionally a database attached to it and optionally a certificate, with defaults suited to the runtime. All of it is already possible with four commands; the difference is what happens when one fails. Four commands give four independent transactions — the database step refuses and you are left with a tenant, a site, and a command that has already exited. This gives one.',
        code: `ratline new node app.example.com --user acme --with-db
ratline new python api.example.com --user acme --app-module app.main:app --asgi
ratline new static www.example.com --user acme --spa --tls --email ops@example.com`,
      },
      {
        title: 'It composes the commands, not the managers',
        body:
          'Every step is the same code path you get by typing it — the same validation, the same refusals, the same messages — so this cannot develop its own idea of what a node site is, and a flag added to `site add` tomorrow is available here tomorrow. The equivalent commands are printed at the end, because this is a shortcut for the common case rather than a replacement for knowing the tool.',
      },
      {
        title: 'What it will not undo',
        body:
          'A tenant that already existed was not this command’s to create, so it is not its to delete. A certificate has already been counted against the rate limit, and revoking it would cost another one to get back where you are. Everything else it made, it removes.',
      },
      {
        kind: 'fix',
        title: 'Deleting a tenant left its system group behind',
        body:
          'nginx is a member of every tenant’s group, so it can read that tenant’s public directory without the world being able to — and userdel will not remove a group that is not empty. So deleting a tenant and creating one with the same name refused with "a group named X already exists … it will not adopt a group it did not create", which is both true and maddening, because ratline had created it. nginx is removed from the group first now.',
      },
    ],
    known: [
      'A static site refuses --with-db: nothing is running to read the connection string.',
      'Next.js standalone binds a port rather than a socket, so those sites need --listen port.',
    ],
  },
  {
    version: 'v0.8.0',
    date: '2026-08-06',
    summary:
      'A published command contract and an MCP server, so an AI agent can drive ratline without guessing a flag or reaching a command it should not.',
    assertions: 315,
    changes: [
      {
        kind: 'feature',
        title: '`ratline schema` publishes the whole command surface',
        body:
          'Every command, every flag with its type and whether it is required, the exit codes with what to do about each, and the shape of the JSON envelope. Generated by walking the command tree, so it describes the binary you are holding rather than a document somebody maintained. An agent that reads it cannot invent --user where the command wants --owner.',
        code: `ratline schema | jq '.. | objects | select(.required == true) | .name'`,
      },
      {
        kind: 'feature',
        title: '`ratline mcp` serves the Model Context Protocol',
        body:
          'Nine read-only tools over stdio — status, site list and show, troubleshoot, logs, doctor, db list, explain, and the schema itself. --allow-mutations adds site deploy and site restart, and nothing else: no site delete, no db drop, no user delete. The protocol is implemented in the binary rather than pulled in, so it stays static and dependency-light.',
        code: `ratline mcp                     # read-only
ratline mcp --allow-mutations   # adds deploy and restart`,
      },
      {
        kind: 'security',
        title: 'Mutating tools are absent, not refused',
        body:
          'Without --allow-mutations they do not appear in tools/list at all. A tool an agent can see is a tool it will eventually try, and a refusal it can retry is an invitation to find a way around it. Called by name anyway, the error names the flag that would enable it, so the agent reports back rather than looping. Every call is audited with its arguments.',
      },
      {
        kind: 'fix',
        title: 'Deleting a tenant left its SSH keys in state',
        body:
          'Removing the home took authorized_keys with it, so the grant stopped working — but the rows stayed, and `key list` went on showing keys for a tenant that no longer existed while `doctor` reported the server clean. A privilege audit that lists grants against a deleted account is worse than one that lists nothing.',
      },
      {
        title: 'The integration suite will not call a degraded run a pass',
        body:
          'Several sections skip themselves when the environment cannot provide something — a Node tarball, a package index. On one run the node section vanished entirely, took nine assertions with it, and the suite printed "306 passed, 0 failed" in green. A floor on the total catches that without needing to know which section went.',
      },
      {
        kind: 'feature',
        title: 'A GitHub Actions guide with a key that can run one command',
        body:
          'Deploy on push, using a narrow sudo grant whose full argument list is pinned: the runner can rsync into one site and run exactly one deploy command as root, and nothing else — not another ratline command, not a shell. Templates for node, python and a static build.',
      },
    ],
    known: [
      'The MCP boundary limits which ratline commands run, not what a deployed application does: deploying code is code execution as that tenant, the same as handing somebody a deploy key.',
      'The curated tool set is deliberately small. An agent reads the schema and tells you what to run for anything outside it.',
    ],
  },
  {
    version: 'v0.7.0',
    date: '2026-08-06',
    summary:
      'Deploying a real Next.js app and a real FastAPI app to a live server found five faults that made both impossible. All fixed.',
    assertions: 315,
    changes: [
      {
        kind: 'fix',
        title: 'No Node project could be built',
        body:
          'Every install was production-only — npm --omit=dev, pnpm --prod, yarn and bun --production, plus NODE_ENV=production, which makes npm skip devDependencies whatever the flags say. Tailwind, TypeScript, PostCSS and Vite all live in devDependencies, so a build failed with "Cannot find module \'@tailwindcss/postcss\'" right after an install that reported success. Dev dependencies are now installed whenever there is a build command, and omitted when there is not.',
      },
      {
        kind: 'fix',
        title: 'The build ran without the site’s environment',
        body:
          'Next.js evaluates route modules while collecting page data, so a build failed with "MONGODB_URI is not set" on code the service would have started with quite happily. Both runtimes now pass the site’s .env to the build — the same variables the service gets — with ratline’s PATH applied last so nothing can redirect the build to a different interpreter. Django’s collectstatic needed the same thing.',
      },
      {
        kind: 'fix',
        title: 'A managed runtime’s npm was not used',
        body:
          'A site pinned to managed Node 24 resolved npm to /usr/bin/npm and failed with "fork/exec /usr/bin/npm: no such file or directory" — on a server with no system Node, which is the server managed runtimes exist for. Build tools now come from the runtime the site is pinned to.',
      },
      {
        kind: 'fix',
        title: 'A site could not be created before its code arrived',
        body:
          '`site add` warned "the application directory is empty … deploy your code, then run site deploy", then started the unit, watched it fail, and rolled the site out of existence — so the advice it printed could never be followed. --repo was the only way in, which rules out a private repository, a build from CI, or an rsync from a laptop. The site is now configured and left stopped.',
      },
      {
        kind: 'fix',
        title: '`--entry .next/standalone/server.js` was refused',
        body:
          'The entry point was validated with the document-root rule, which forbids a leading dot because nginx denies hidden files — correct for a directory nginx serves, irrelevant for a file node executes. That is the path in ratline’s own Next.js guide, so the guide had never been run. Nuxt’s .output and SvelteKit’s .svelte-kit were refused for the same reason.',
      },
      {
        kind: 'security',
        title: 'One tenant’s socket directory could lock out every other',
        body:
          'paths.run_dir is the shared parent of every site’s socket directory. Staging a mongosh script created it 0750 root-owned, so on a server where `db ping` ran before the first socket site — the normal order — every later tenant failed to bind with "[Errno 13] Permission denied" and nginx answered 502. Staging moved into a private 0700 subdirectory, the parent is kept traversable and reinstated on every unit start, and a tmpfiles rule re-establishes it after each boot, since /run is tmpfs.',
      },
      {
        kind: 'fix',
        title: '`runtime install python` accepted an interpreter that cannot make a virtualenv',
        body:
          'Debian and Ubuntu split venv into python3.12-venv, so the interpreter is present and `python -m venv` fails. ratline reported the runtime ready and the failure surfaced three commands later, out of site add, after it had rolled the site back. Checked at install time now, and the package installed.',
      },
      {
        kind: 'feature',
        title: 'Two guides written by doing it',
        body:
          'Deploy a Node app and Deploy a Python app: bare server to a running site with a database and TLS, every command in the order you run it, written from the deployment that found the faults above rather than from the command reference.',
      },
    ],
    known: [
      'Next.js standalone binds a port rather than a Unix socket, so those sites need --listen port. Sites that can speak a socket should keep the default.',
      'Dev dependencies stay on disk after a build: a wrong prune fails at request time rather than at deploy time.',
      'DNS-01 is covered against a challenge test server, not a real DNS provider’s API.',
    ],
  },
  {
    version: 'v0.6.1',
    date: '2026-08-06',
    summary:
      'The menu no longer walks you into a command it never asked the questions for.',
    assertions: 310,
    changes: [
      {
        kind: 'fix',
        title: 'The menu asks for required flags instead of offering them',
        body:
          '`--owner` and `--database` sat in the list of optional extras, so you could take the defaults, read a summary, confirm, and watch the command refuse for something the menu never mentioned. Required-ness was enforced by hand in each command — the messages are worth writing — but nothing declared it where the interactive layer could read it. There is a marker now, applied to all thirteen, and a test that reads the source for both forms of enforcement and fails if any is unmarked.',
      },
      {
        kind: 'fix',
        title: 'The menu hands over to a command’s own wizard',
        body:
          '`user add`, `site add`, `cert issue` and `key add` have wizards that know things the generic flag picker cannot — site add sniffs the project to suggest a runtime, user add offers ~/.ssh/id_ed25519.pub and checks it is there. The menu collected their flags generically instead, so the four commands with the best interactive support gave the worst experience through the menu.',
      },
      {
        kind: 'fix',
        title: 'A pasted public key is taken as a key',
        body:
          'It was read as a filename, and the error named "no such file: /root/ssh-ed25519 AAAAC3Nz… ark@ark". Asked for a key and handed a key, taking it is the only sensible reading — and at a prompt, pasting is what everybody does. A public key is not a secret, so unlike a password there is no reason to keep it out of argv; it is parsed and validated identically either way. The prompt also names the kind of value a flag wants now, which is how somebody ended up pasting a key into a flag expecting a path.',
        code: `ratline key add --scope user --user acme --label laptop \\
  --key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop'`,
      },
      {
        kind: 'security',
        title: 'A pasted private key is called a private key',
        body:
          'A private key is several lines, so the multi-line check matched before the one that recognises it — answering "the key spans more than one line", which is true and buries the only thing that matters. It now says what happened and tells you to rotate it.',
      },
    ],
    known: [
      'The menu collects flags, not positional arguments: cobra validates those before any prompt could run.',
      'DNS-01 is covered against a challenge test server, not a real DNS provider’s API.',
      'ratline db manages MongoDB only.',
    ],
  },
  {
    version: 'v0.6.0',
    date: '2026-08-06',
    summary:
      'Nothing asks you to pipe a secret through printf any more. Every command is reachable from a menu, and -i works on all of them.',
    assertions: 303,
    changes: [
      {
        kind: 'fix',
        title: '`db connect` asks for the connection string',
        body:
          'Reported from a real server: `printf` reads a % in a password as a format verb, so the string arrived truncated with no host in it, and the error blamed /etc/ratline/db/mongodb.uri — a file that command had not written, in the same breath as saying nothing was stored. Recommending printf for a secret was always wrong; the rule that matters is that it must not be in argv or the shell history, and a prompt satisfies that without putting a format-string interpreter in the way. `db connect` with no flags now prompts, with echo off. --stdin and --from-file remain for automation.',
        code: `ratline db connect
MongoDB admin connection string (not echoed): ▏`,
      },
      {
        kind: 'fix',
        title: 'It validates the string before writing it, and says what is wrong',
        body:
          'connect checked only the mongodb:// prefix and never parsed, so a mangled string was stored and rejected later by the code that reads it back. Both paths share one validator now, applied before anything is written. And "invalid port after host" — url.Parse’s wording — sends you to look at ports, when the real problem is that there is no @ and therefore no host. The message says that, and names the cause.',
      },
      {
        kind: 'security',
        title: '`site env set` no longer needs the secret in argv',
        body:
          'Its declared usage was `set <domain> KEY=VALUE`, so the primary documented form put the value in argv — world-readable through /proc for as long as the command runs, then in your shell history. A bare KEY now means "ask me", which used to be a usage error. KEY=VALUE still works and is the clearer thing to write for LOG_LEVEL=info.',
        code: `ratline site env set api.example.com DATABASE_URL
DATABASE_URL (not echoed): ▏`,
      },
      {
        kind: 'feature',
        title: 'The menu covers every command, because it is generated',
        body:
          'Bare `ratline` on a terminal opened a hand-written menu listing five groups with two or three verbs each — about a dozen of the eighty-six commands, with the rest unreachable unless you already knew they existed. It walks the command tree now: 99 commands, each with its own help text and its own flags, taken from the same place the command parses them. Options are a list rather than twenty forced questions, and it prints the equivalent command before running anything.',
        code: 'ratline',
      },
      {
        kind: 'feature',
        title: '-i works on every command, and asks for missing flags',
        body:
          'The global -i said "prompt for whatever was not supplied as a flag" and was read by four commands. It now offers any command’s own options, and a command that refuses for a missing required flag asks for it instead — writing the answer into the flagset, so what runs is exactly what would have run had you typed it. Unchanged without a terminal, or under --no-input or --json: a script that starts asking questions is a script that hangs.',
      },
      {
        title: 'The connection-string file explains itself',
        body:
          'The whole file used to be trimmed and used as the URI, so the first instinct on creating a credential in /etc — a line saying what it is — broke it. Blank lines and # comments are skipped now, and `db connect` writes a header for whoever finds the file during an audit. Two connection strings in one file is an error rather than a guess.',
      },
      {
        kind: 'security',
        title: 'The sudo grant path has tests',
        body:
          'GrantSudo is the one function that can hand a tenant root and nothing was testing it — not the unit tests, not the integration suite. Behaviour unchanged; what is new is that each guard is proved by breaking it and watching a test fail, and that the suite runs a real visudo and then asks sudo itself, via `sudo -l`, whether the rule is as narrow as ratline claims.',
      },
    ],
    known: [
      'The interactive menu collects flags, not positional arguments: cobra validates those before any prompt could run, so `ratline site show` still needs its domain on the command line.',
      'DNS-01 is covered against a challenge test server, not a real DNS provider’s API.',
      'ratline db manages MongoDB only.',
      'No whole-server restore: `restore` handles one site or one tenant.',
    ],
  },
  {
    version: 'v0.5.0',
    date: '2026-08-06',
    summary:
      'Database listing stops hiding databases, `ratline config` edits the config without breaking it, and setting MongoDB up is one command.',
    assertions: 289,
    changes: [
      {
        kind: 'fix',
        title: '`db list --live` hid the databases it exists to find',
        body:
          'The filter that skips MongoDB’s own admin, local and config databases was also skipping anything ratline had not provisioned — which is the entire reason to pass --live. A database created by hand, or by an older tool, is exactly what you are looking for when you ask the server rather than the index: nothing will revoke its users when the tenant is deleted. It is now listed and marked as unmanaged.',
        code: 'ratline db list --live',
      },
      {
        kind: 'feature',
        title: '`ratline config` reads and writes the configuration',
        body:
          'show, get, set, unset, edit, validate, reference and path. Every change is validated before it is committed, so a value that would not load leaves the previous file exactly as it was and the error names the setting. The editor is textual rather than a re-encode, which means your comments survive — the shipped defaults.yaml is the reference, and flattening it was a real thing that used to happen.',
        code: `ratline config set defaults.memory_max 768M
ratline config get acme.email
ratline config validate`,
      },
      {
        kind: 'feature',
        title: 'Setting up databases is one command, not four',
        body:
          '`db connect` writes the admin connection string at 0600, creates its directory at 0700, turns provisioning on and proves the credentials work — and if any of that fails, nothing is left behind. Two of the four manual steps it replaces were about the mode of a file holding the root password for every database on the server. `db enable` and `db disable --forget` handle the rest of the lifecycle.',
        code: `ratline db connect
ratline db ping`,
      },
      {
        kind: 'security',
        title: 'The sudo grant path is tested now',
        body:
          'GrantSudo is the one function in ratline that can hand a tenant root, and nothing was testing it — not the unit tests, not the integration suite. The behaviour has not changed: still config-gated, still one command at a time, still the full argument list pinned so systemctl cannot be handed arbitrary arguments, still validated with visudo before installation. What is new is that each of those is now proved by breaking it and watching a test fail, and that the integration suite runs a real visudo and then asks sudo itself, with `sudo -l`, whether the rule is as narrow as ratline claims.',
      },
      {
        title: 'The documentation site is organised by subject',
        body:
          'Every command has its own page rather than being one of fourteen stacked on a group page, and each sidebar section is a subject that owns everything about itself — its commands, the concepts behind them, the in-depth pages, the runbooks, and the configuration settings that change how it behaves. The thirteen `ratline explain` topics are on the site for the first time and searchable to the sentence: roughly 7,000 words that previously existed only behind a terminal command.',
      },
    ],
    known: [
      'DNS-01 is covered by the integration suite against a challenge test server, not against a real DNS provider’s API.',
      'ratline db manages MongoDB only. There is no PostgreSQL or MySQL provisioning.',
      'No whole-server restore: `restore` handles one site or one tenant, so rebuilding from nothing still means restoring /var/lib/ratline/state.db and /etc/ratline yourself, then `ratline reconcile --fix`.',
    ],
  },
  {
    version: 'v0.4.0',
    date: '2026-08-05',
    summary:
      'MongoDB databases and users, with roles that cannot reach past their own database.',
    assertions: 236,
    changes: [
      {
        kind: 'feature',
        title: '`ratline db` provisions MongoDB',
        body:
          'Create a database, create a user whose only role is on that database, hand the connection string to a site, rotate it, re-role it, revoke it. Twelve commands: ping, create, list, show, drop, roles, and user add/list/password/grant/delete.',
        code: `ratline db create shop --owner acme --attach shop.example.com
ratline db user add reports --database shop --role read
ratline db user password shop_app --all-sites`,
      },
      {
        title: 'It provisions inside a server rather than installing one',
        body:
          'A database server is stateful, with backups and a replication topology, and a tool that silently apt-gets one has decided something belonging to whoever owns the data — the same reasoning that has ratline configure nginx and drive certbot without installing either. A local mongod and a managed cluster differ only in the connection string, which lives in a 0600 file rather than in config.yaml because it is the root password for every database on the server.',
      },
      {
        kind: 'security',
        title: 'No operator input is ever interpolated into JavaScript',
        body:
          'Every operation runs one static file, embedded in the binary, as `mongosh --nodb --quiet --file`. Values arrive through the environment. The alternative is building an --eval string, and then a username containing a quote closes it and runs whatever follows, as root, against a server holding every tenant’s data. There is no escaping to get right because there is no string to escape into. --nodb matters for the same reason: mongosh normally takes the connection string as its first argument, and /proc/PID/cmdline is world-readable.',
      },
      {
        kind: 'security',
        title: 'Every role is scoped to one database',
        body:
          'read, readWrite, dbAdmin, dbOwner. The cluster-wide roles are absent by construction — granting readWriteAnyDatabase to a tenant’s application hands it every other tenant’s data, and it would be one flag away if the list were open. The suite proves it rather than asserting it: a credential writes its own database, is refused on another, and a demotion from readWrite back to read takes the write away again.',
      },
      {
        title: 'A password is shown once, and never stored',
        body:
          'MongoDB keeps a hash and will not return it, so one cannot be displayed later even if that were wanted — which is the right shape rather than a limitation. --attach writes the URI into the site’s .env at 0600 instead of onto a terminal, because shell history and scrollback outlive every rotation. --all-sites updates every site holding a credential when it is rotated, which is the difference between a rotation and an outage; a site that could not be updated is named loudly, since by then the old password has already stopped working.',
      },
      {
        title: '`doctor` knows about the database',
        body:
          'It reports an unreachable server, an admin file at the wrong mode, a server not enforcing authentication, a database recorded here but missing there, and — the one that matters — a user a site still holds credentials for but which the server no longer has. That last case is an application failing to authenticate right now.',
      },
      {
        kind: 'fix',
        title: 'The documentation stopped claiming this did not exist',
        body:
          'Four places said database provisioning was absent or that `ratline db` was a stub, including a non-goal and a bullet on the home page. A CI check now verifies every internal link on this site resolves, after adding this section nearly shipped a link to a page that was never written.',
      },
    ],
    known: [
      'MongoDB only. Postgres, MySQL and Redis are not supported, and the shape here does not assume they will be.',
      'No whole-server restore. `restore` handles a site or a tenant; rebuilding a server still means backing up /var/lib/ratline/state.db and /etc/ratline yourself.',
      'No DNS or mail management, and no PHP runtime.',
    ],
  },
  {
    version: 'v0.3.0',
    date: '2026-08-05',
    summary: 'One-command install, and ratline’s own units now travel inside the binary.',
    upgrade: 'curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh',
    assertions: 184,
    changes: [
      {
        kind: 'feature',
        title: 'One command on a bare server',
        body:
          'Resolves the latest release, downloads for this architecture, verifies against the release’s own SHA256SUMS, installs, and runs `ratline init` — configuration, directory layout, and the renewal and key-pruning timers started.',
        code: 'curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh',
      },
      {
        title: 'The renewal units moved into the binary',
        body:
          'The installer could not have been piped before, for a structural reason: it installed the systemd units by copying packaging/systemd/, so it only worked with a checkout or an unpacked tarball around it. The units now live in the embedded templates and `ratline init` installs them — so a server that received nothing but the binary renews its certificates, which was not previously true of `make install` either. A unit you have edited is left alone and reported.',
      },
      {
        kind: 'fix',
        title: '`doctor` no longer tells you to delete your renewal timer',
        body:
          'It reported ratline’s own units as "a ratline unit with no matching site" and offered, as the fix, a command that removes them. Following that advice stops certificates renewing, and the first sign is an expired certificate weeks later. Survivable while only install.sh placed those units; with `init` doing it, every server would have been given that advice.',
      },
      {
        kind: 'fix',
        title: 'A failed /dev/tty probe killed the installer',
        body:
          'A failed redirection on `exec` terminates a non-interactive shell, so on any host where /dev/tty exists but cannot be opened — a container, for instance — the script died partway through, after printing a raw error beneath a question it had already asked.',
      },
      {
        kind: 'fix',
        title: 'Builds are reproducible',
        body:
          'The build stamped the wall clock into the binary, so two builds of the same commit differed and rebuilding a tag could not reproduce the published artefacts — removing the one check on a release that does not require trusting whoever uploaded it. From v0.3.0 onward, `make dist` at a tag reproduces the published files byte for byte.',
        code: 'git checkout v0.4.0 && make dist && sha256sum -c dist/SHA256SUMS',
      },
      {
        kind: 'fix',
        title: '`ratline restor --help` reports the typo',
        body:
          'cobra handles the help flag before the unknown-command check, so a misspelling printed the root help and exited 0 — leaving you reading a list of commands, wondering which one you got wrong. Without --help the same typo correctly exits 2, which is why it went unnoticed.',
      },
    ],
  },
  {
    version: 'v0.2.0',
    date: '2026-08-05',
    summary: 'Two of v0.1.0’s three known gaps closed: restore, and DNS-01.',
    assertions: 184,
    changes: [
      {
        kind: 'feature',
        title: '`ratline restore`',
        body:
          'The counterpart backup never had. The hard part is not extraction — it is everything the archive does not contain: the state row, the vhost, the unit, the tenant’s uid, the port. So restore rebuilds the row from the manifest that travelled with the files, re-renders the vhost and unit rather than restoring them, takes ownership from the account as it exists on this server, reallocates the port, and then proves the site serves before reporting success.',
        code: 'ratline restore /var/backups/ratline/app.example.com-20260105T120000Z.tar.gz',
      },
      {
        kind: 'security',
        title: 'Archives are treated as untrusted',
        body:
          'It extracts as root, and an archive may have been copied between servers or handed over by whoever is migrating in. A member with an absolute or traversing path is refused rather than sanitised, symlinks are chowned with lchown rather than followed, the manifest’s domain and owner are validated as if typed, and the slug is recomputed rather than trusted because it names the systemd unit.',
      },
      {
        kind: 'feature',
        title: 'DNS-01 through a provider certbot has no plugin for',
        body:
          '--dns-provider manual with a hook script. certbot ships plugins for around a dozen providers; for everything else — and for a company’s internal DNS — this is the only route to DNS-01 at all, and DNS-01 is the only route to a wildcard. The hook runs as root with the validation token in its environment, so it is refused unless it is an absolute path, owned by root, executable, and not writable by group or other.',
        code: `ratline cert issue '*.example.com' \\
  --dns-provider manual \\
  --dns-hook /etc/ratline/dns/publish.sh \\
  --dns-cleanup-hook /etc/ratline/dns/withdraw.sh`,
      },
      {
        kind: 'fix',
        title: 'The site manifest is readable',
        body:
          '.ratline/site.yaml was always written with a comment claiming it existed "so a site survives the loss of the state database" — and nothing read it, so it did not. It is parsed now, which is what makes restore possible and that claim true.',
      },
    ],
  },
  {
    version: 'v0.1.0',
    date: '2026-08-05',
    summary: 'The first release.',
    assertions: 137,
    changes: [
      {
        kind: 'feature',
        title: 'Tenants, sites, TLS and diagnosis',
        body:
          'A system account per tenant. An nginx vhost and a systemd unit per site, for static, Node and Python. TLS as a resource with its own lifecycle, including a preflight that checks DNS, port 80, server_name conflicts and the rate-limit budget before spending an attempt. And a diagnosis that walks the dependency chain and stops at the cause rather than listing symptoms.',
      },
      {
        title: 'Licensed under AGPL-3.0',
        body:
          'The repository was public with no licence, which means all rights reserved: nobody could legally use or contribute to it. AGPL rather than GPL for one clause — ratline is the provisioning core of a hosting panel, so the obvious way to take it without giving anything back is to wrap it in a panel and sell access, which is network use rather than distribution.',
      },
    ],
    known: [
      'backup had no restore, and DNS-01 had no automated coverage. Both closed in v0.2.0.',
      'These binaries predate reproducible builds and cannot be reproduced from the tag.',
    ],
  },
];
