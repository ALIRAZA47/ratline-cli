---
name: ci-github-actions-django
tags: [ci]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Write, Skill]
---
Set up GitHub Actions so my Django site api.acme.example deploys on every push to main. It's already provisioned
with ratline under the acme tenant (Django, with --manage-py). Write the workflow to .github/workflows/deploy.yml
in this repo, and put the commands I have to run once on the server into SERVER.md. Do not hand me a root key
approach.

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server; use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
