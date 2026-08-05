<!--
The diff shows what changed. Use this to say what was wrong and what it cost, so that
someone reading git log in a year understands the reasoning rather than re-deriving it.
-->

## What was wrong

## What this changes

## How it was verified

<!-- Which of these you ran, and anything you could not run and why. -->

- [ ] `make test` — unit tests, race detector
- [ ] `make lint`
- [ ] `make integration` — Docker; ~10 minutes
- [ ] A test that fails without this change

## Properties preserved

<!--
Tick what applies. These are the promises the tool makes, and a change that breaks one
needs to say so explicitly rather than quietly. CONTRIBUTING.md explains each.
-->

- [ ] No shell strings — every external command is an argv slice
- [ ] Secrets never in argv
- [ ] Nothing that belongs to a tenant is installed as root
- [ ] Failures unwind: the server is left as it was, still serving
- [ ] Safe to run twice
- [ ] `--dry-run` writes nothing
- [ ] No file lacking `# managed-by: ratline` is overwritten
- [ ] Not applicable — this touches none of the above

## Anything left out

<!-- Known gaps, follow-ups, or things you noticed but deliberately did not fold in. -->
