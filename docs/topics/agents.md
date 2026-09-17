# Driving ratline from an AI agent

> A published command contract, an MCP server that is read-only until you say otherwise,
> and a plugin of skills and agents that are callers of the CLI and nothing more.

An agent asked to deploy something will read `--help`, infer a flag, and run it as root.
The failure mode is not that it refuses; it is that it confidently invents `--user` where
the command wants `--owner`, or retries a certificate issuance that has already burned
four of five attempts this hour. Both are the tool's fault for making them guessable, so
ratline gives an agent three things instead.

## The contract

    ratline schema

Every command, every flag with its type, default and whether it is required, the exit
codes with what to do about each, and the shape of the envelope, as JSON. It is produced by
walking the command tree, so it describes the binary in front of you rather than a document
somebody maintained. A flag added tomorrow is in it tomorrow; one that is renamed cannot
leave a stale entry behind.

`--json` puts one object on stdout and every log line on stderr, and the exit codes are a
contract: 5 means wait and retry, 6 means the rollback itself failed and a human is needed,
8 and 9 mean do not retry a certificate. `--dry-run` writes nothing at any layer, so an
agent can be asked to rehearse and show the plan at no cost.

## The MCP server

    ratline mcp
    ratline mcp --allow-mutations

Speaks the Model Context Protocol over stdin and stdout, so an agent calls tools instead of
parsing prose. Read-only by default: status, site list and show, troubleshoot, logs,
doctor, jobs, database list, schema and these explainer topics. The mutating tools are
**absent** from the list rather than present and refusing, because a tool an agent can see
is a tool it will eventually try. `--allow-mutations` adds exactly two: `site deploy` and
`site restart`. Nothing that deletes.

From a laptop it runs over SSH, which is how an agent there reaches this server:

    { "command": "ssh", "args": ["-T", "root@server", "sudo", "ratline", "mcp"] }

Every call is written to the audit log with its arguments.

## The plugin

The repository ships a Claude Code plugin at `plugins/ratline`, installable from the
repository as a marketplace:

    /plugin marketplace add ALIRAZA47/ratline-cli
    /plugin install ratline@ratline
    export RATLINE_HOST=root@203.0.113.5

It contains the read-only MCP configuration above, ten **skills** (deploy any application,
wire CI, set a server up, diagnose, secrets, access, databases, jobs, day-two operations,
the web panel) and five **agents**: a deployer that dry-runs everything and asks before
anything irreversible, an on-call responder that holds only the read-only tools and so
cannot change the server even if asked, an operator for maintenance passes, an access
administrator for keys and tenants, and a reviewer that checks a change to ratline itself
against the project's invariants.

The skills follow the open Agent Skills format, so another agent can use them by copying
a skill's directory. They are written the way the web panel is: as callers. They look
flags up in `ratline schema` rather than remembering them, rehearse with `--dry-run`, send
secrets on stdin, never hand-write an nginx file or a unit, and say plainly when ratline
cannot host something rather than improvising. Every command and flag they name is
checked against `ratline schema` in the project's CI, so a release that renames a flag
fails the build rather than teaching an agent a flag that no longer exists.

## What this is not

It is a boundary, not a sandbox. An agent with the mutating tools can deploy code, and
deploying code is arbitrary code execution as that tenant, the same as handing someone a
deploy key. The gate limits which ratline commands run, not what the deployed application
does. Run it over SSH with a key that reaches one server, keep `--allow-mutations` off for
anything exploratory, and give the tenant of an untrusted application its own account.

See also: `ratline explain safety`, `ratline explain diagnose`.
