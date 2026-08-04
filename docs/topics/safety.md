# Safety guarantees

> What ratline promises about running twice, failing halfway, and what it refuses.

## Every command is safe to run twice

Running `site add` for a site that already exists reports that and changes nothing.
The same is true of `user add`, `key add` and `cert issue`. A provisioning tool that
is not idempotent cannot be used from a script, and a script is how servers are
actually built.

## A failed mutation leaves nothing behind

Every mutation is staged and pushed onto a rollback stack. A failure at any step
unwinds the ones before it: the user is removed, the directories are removed, the
vhost is removed, the unit is removed, the port is released. Ctrl-C unwinds the same
way, because the interrupt reaches the running step through its context rather than
abandoning it.

## --dry-run prints, and nothing else

    ratline site add app.example.com --user acme --runtime node --entry server.js --dry-run

Every mutating command accepts it. It prints the commands, the files and their
contents, and touches nothing.

## One mutation at a time

A global lock at `/run/ratline.lock` serialises mutating commands, so two operators
— or an operator and a cron job — cannot interleave halfway through building the
same site. Read-only commands never take it.

## What ratline refuses to do

* Run as anything but root, or run from a binary that is group- or world-writable.
* Overwrite a file at one of its own paths that lacks the `# managed-by: ratline`
  header and a corresponding state row.
* Grant sudo to a tenant unless configuration allows it, and then only after
  `visudo -c` validates the result.
* Add a key to root's `authorized_keys`, unless configuration allows it and it is
  asked for explicitly.
* Build a shell command string. Everything is an argv list, so a domain, a label or
  a path can never be reinterpreted as a shell operator.
* Enable HSTS on a self-signed or staging certificate.
* Change `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` or `Port` without
  an explicit flag and a typed confirmation.

## Exit codes are a contract

    0  success
    1  unclassified failure
    2  usage error — bad flags, bad arguments, failed validation
    3  precondition unmet — including "this needs root"
    4  an external command failed
    5  another ratline invocation holds the lock
    6  the operation failed and so did its rollback
    7  it started, but never became healthy
    8  an ACME challenge failed
    9  it would exceed a certificate authority's rate limit
   10  input was required but stdin is not a terminal

A script can branch on these. Exit 6 is the one to notice: the rollback itself
failed, so the server is in a state ratline could not restore and needs looking
at before anything is retried.

See also: `ratline explain state`.
