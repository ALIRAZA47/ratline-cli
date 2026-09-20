# The ratline plugin for Claude Code

Skills and agents for operating a server that runs [ratline](https://ratline.alirazakhan.me),
plus a read-only MCP server that reaches it over SSH. Nothing in here reimplements ratline:
every skill and every agent is a *caller* of the CLI, the same way the web panel is. They
look flags up in `ratline schema` rather than remembering them, rehearse every mutation with
`--dry-run`, send secrets on stdin, and branch on the exit-code contract.

## Install

```
/plugin marketplace add ALIRAZA47/ratline-cli
/plugin install ratline@ratline
```

Then tell the plugin which server you mean. `RATLINE_HOST` is an SSH destination, either
`root@203.0.113.5` or an alias from your `~/.ssh/config`. It is read when the MCP server
starts and by every skill:

```bash
export RATLINE_HOST=root@203.0.113.5
```

The account has to be able to run `sudo ratline` without a password prompt: root, or an
admin account holding a global-scope key. SSH is started with `BatchMode=yes`, so a
password prompt fails fast instead of hanging.

## What it gives Claude

**A read-only MCP server.** `ratline mcp` over SSH, exposing status, site list and show,
troubleshoot, logs, doctor, jobs, database list, schema and the explainer topics. It does
not expose a single mutating tool. That is ratline's own gate, not the plugin's: the
mutating tools are absent from the server unless it is started with `--allow-mutations`,
and this plugin never starts it that way. Anything that changes the server goes through
the CLI over SSH, where `--dry-run` and the audit log apply.

**Ten skills**, loaded when the task matches:

| Skill | For |
|---|---|
| `ratline-deploy` | Put an application of any kind live: read the repo, provision, ship, verify |
| `ratline-ci` | Deploy on push from GitHub Actions or any CI, with a key that can run one command |
| `ratline-server-setup` | A bare Ubuntu or Debian VPS to a working ratline server |
| `ratline-diagnose` | A site, tenant, key, certificate, nginx or sshd that is broken |
| `ratline-secrets` | Environment variables and secrets, safely |
| `ratline-access` | SSH keys and tenants: onboarding, offboarding, rotation |
| `ratline-databases` | MongoDB, MySQL and Redis databases and their users |
| `ratline-jobs` | Scheduled jobs and background workers instead of a crontab |
| `ratline-operate` | Day-two: doctor, drift, backup and restore, upgrades, moving a server |
| `ratline-panel` | Installing and running the web panel |

**Five agents**, for when the work is a job rather than a question:

| Agent | Can it change the server? |
|---|---|
| `ratline-deployer` | Yes, after a dry run and your confirmation for anything irreversible |
| `ratline-oncall` | No. It has only the read-only MCP tools, so it cannot even if asked |
| `ratline-operator` | Yes, for maintenance; upgrades, drift repair and restores need a typed yes |
| `ratline-access-admin` | Yes, for keys and tenants; never the last admin key, never sshd's core settings |
| `ratline-invariant-reviewer` | No. It reviews a change to ratline itself against the project's invariants |

## Using the skills with another agent

The `skills/` directory follows the open Agent Skills format (`SKILL.md` with `name` and
`description` frontmatter, references beside it). Copy a skill's directory into wherever
your agent loads skills from. The skills assume a shell with `ssh` and, for the deploy and
CI skills, `dig`, `rsync` and `jq`.

## Trust model

Deploying code is arbitrary code execution as that tenant, the same as handing someone a
deploy key. The MCP gate limits which ratline commands an agent can run, not what the
deployed application does. Use an SSH key that reaches one server, keep the read-only
agent for anything exploratory, and read [the security model](https://ratline.alirazakhan.me/concepts/security)
for where the isolation stops.

## Evaluating the skills

`evals/` holds nine cases for `claude plugin eval`: four deploys (a Next.js standalone
shop, a FastAPI service with Redis and an external Postgres, a Vite SPA, and a Laravel app
that ratline must refuse), a 502 diagnosis from a captured `troubleshoot` transcript, a
GitHub Actions pipeline for a Django site, a contractor's site-scoped key, a leaked
database password, and a nightly job. Each case scaffolds a fixture repository and the real
`ratline schema` output into the workspace, tells the agent the server is unreachable, and
asks for the exact commands. Regex graders check the properties that matter (the right
runtime and flags, `--dry-run`, secrets on stdin, nothing hand-written), an LLM judge
checks the shape, and the run also reports a no-plugin baseline arm.

```bash
claude plugin eval ./plugins/ratline --scaffold --allow-tools Write --trust-plugin --no-publish
claude plugin eval ./plugins/ratline --case 'deploy-*' --runs 1 --ablation none --scaffold --allow-tools Write --trust-plugin --no-publish
```

The schema snapshot is generated, not captured: `make eval-fixture` writes
`evals/fixtures/ratline-schema.json` from the built binary, and CI regenerates it and fails
on a diff, exactly as it does for `docs/reference/commands.md`. It was a hand-made v0.18.0
capture while the skills are checked against the current binary, so an agent that read a
skill and then checked it against this file — which every prompt tells it to do — was told
the command it had just been taught does not exist. The `version` field is a fixed label
rather than the binary's version, which comes from `git describe` and would differ between
CI's shallow checkout and yours — so the check fires when the command surface moves and not
on every commit. No prompt names a version any more: nine copies of a fact that goes stale
is the same problem one directory down.

`--scaffold` is required because the fixtures are copied in by each case's `scaffold.sh`;
`--allow-tools Write` is for the CI case, which writes a workflow. Results land under
`evals/results/`, which is ignored by git. A run costs roughly a dollar per case per arm,
so start with `--runs 1`. The default judge is haiku, which is noisy on multi-part rubrics; `--judge-model sonnet` is worth the small extra cost. **What a run does and does not tell you.** A single run per case is noisy: across four
paired with/without iterations of this suite, the same case scored anywhere within 0.11 to
0.30 of itself under an identical configuration, because the answers are generated text and
the judge sees a different one each time. The suite mean over nine cases is the stable
number; a single case moving is usually not a signal. `--runs 3` (the tool's own default)
is what to use before believing a per-case change, at three times the cost.

Measured over those four iterations, the plugin was worth about +0.14 on the suite mean,
and the value was concentrated rather than spread: the refusal case (+0.57) and the CI
access model (+0.43) carry most of it, because those have a specific ratline-shaped answer
a model does not otherwise know. Five of the nine cases measured near zero, which is honest
to record: for ordinary deploy reasoning the skills mostly agree with what a capable model
already does, and their value there is consistency rather than correctness.

An LLM judge cannot verify that a flag exists, so that is not in any rubric; after a run with `--keep-temp`, `python3 plugins/ratline/evals/analyze.py <aggregate-result.json> ./bin/ratline .` recovers each arm's final message and checks every `ratline` command it proposed against `ratline schema`, which is the number that matters most for something an agent would run as root. The cases never grant Bash: the agent reads the schema with Grep,
and on a machine whose Docker credential store contains a symlink the runner refuses
Bash-granting evaluations anyway.

## Keeping it honest

Every `ratline …` command and `--flag` named in a skill or agent is checked against
`ratline schema` in CI (`make check-skills`), the same way the generated command reference
is. A release that renames a flag fails the build here rather than teaching an agent a
flag that no longer exists.
