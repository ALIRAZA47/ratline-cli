#!/usr/bin/env python3
"""Post-hoc analysis of a `claude plugin eval` run of the ratline plugin.

Reads the aggregate JSON, prints a per-case with/without table, then walks each run's
kept trace to recover the agent's final message and validates every `ratline …` command
it proposed against `ratline schema`, using the same logic as scripts/check-skills.py.
That is the number the LLM judge cannot produce reliably: how many of the commands an
agent would have run as root actually exist.

    python3 plugins/ratline/evals/analyze.py <aggregate-result.json> ./bin/ratline .

Run the suite with --keep-temp so the traces are still on disk; the kept directories are
mode 000 until you `chmod 700` them (the runner says so when it finishes).
"""
import json, os, re, sys, importlib.util, pathlib
from collections import Counter, defaultdict

agg, binary, root = sys.argv[1], sys.argv[2], pathlib.Path(sys.argv[3])
spec = importlib.util.spec_from_file_location("cs", root / "scripts/check-skills.py")
cs = importlib.util.module_from_spec(spec); spec.loader.exec_module(cs)
surface = cs.Surface(cs.load_schema(binary))

def final_text(trace_path):
    """The last assistant text in a trace.jsonl, best effort across event shapes."""
    if not trace_path or not os.path.exists(trace_path):
        return None, Counter()
    tools = Counter(); last = ""
    for line in open(trace_path, errors="replace"):
        try: ev = json.loads(line)
        except Exception: continue
        def walk(o):
            nonlocal last
            if isinstance(o, dict):
                if o.get("type") == "tool_use" and o.get("name"): tools[o["name"]] += 1
                if o.get("type") == "text" and isinstance(o.get("text"), str) and len(o["text"]) > 40:
                    last = o["text"]
                for v in o.values(): walk(v)
            elif isinstance(o, list):
                for v in o: walk(v)
        walk(ev)
    return last, tools

def validate(text):
    """Every ratline invocation in fenced/inline code of the text, checked against the schema."""
    problems = []
    lines = list(cs.code_lines(text))
    n = 0
    for ln, line in lines:
        if "ratline" not in line: continue
        n += 1
        cs.check_tokens(cs.split_line(line), surface, f"line {ln}", problems)
    return n, problems

r = json.load(open(agg))
print(f"claude {r.get('claudeVersion')}  cost ${r.get('costUsd'):.2f}  {r.get('durationSeconds')}s  partial={r.get('partial')}")
print(f"{'case':30} {'with':>6} {'without':>8} {'delta':>7}  skill-fired  invalid-cmds(with/without)")
tot = defaultdict(list)
detail = {}
for c in r["cases"]:
    row = {}
    for arm in ("with", "without"):
        runs = c["arms"].get(arm, [])
        if not runs: continue
        run = runs[0]
        row[arm] = run
        text, tools = final_text(run.get("tracePath"))
        n, probs = validate(text or "")
        detail[(c["name"], arm)] = (text, tools, n, probs, run)
        tot[arm].append(run.get("score") or 0)
    w = row.get("with", {}); wo = row.get("without", {})
    sf = next((g.get("passed") for g in w.get("graders", []) if g.get("name") == "skill-fired"), None)
    iw = len(detail.get((c["name"], "with"), (None, None, 0, [], None))[3])
    iwo = len(detail.get((c["name"], "without"), (None, None, 0, [], None))[3])
    print(f"{c['name']:30} {w.get('score', 0):6.2f} {wo.get('score', 0):8.2f} {(w.get('score', 0) - wo.get('score', 0)):+7.2f}  {str(sf):11}  {iw}/{iwo}")
for arm in ("with", "without"):
    if tot[arm]: print(f"mean {arm:8} {sum(tot[arm])/len(tot[arm]):.2f}")

print("\n=== grader failures in the WITH arm (what to fix in the skills, or in the graders) ===")
for c in r["cases"]:
    for run in c["arms"].get("with", []):
        fails = [g for g in run.get("graders", []) if not g.get("passed") and g.get("name") != "skill-fired"]
        if fails:
            print(f"- {c['name']}: " + ", ".join(g["name"] for g in fails))

print("\n=== invalid ratline commands proposed (both arms) ===")
for (name, arm), (text, tools, n, probs, run) in sorted(detail.items()):
    if probs:
        print(f"- {name} [{arm}] ({n} invocations, {len(probs)} problems):")
        for p in probs[:8]: print("    ", p)

print("\n=== tools used ===")
for (name, arm), (text, tools, n, probs, run) in sorted(detail.items()):
    print(f"- {name} [{arm}] turns={run.get('turns')} tools={dict(tools)}")

out = pathlib.Path(agg).with_suffix(".messages.md")
with open(out, "w") as f:
    for (name, arm), (text, tools, n, probs, run) in sorted(detail.items()):
        f.write(f"\n\n# {name} [{arm}] score={run.get('score')}\n\n{text or '(no final text recovered)'}\n")
print(f"\nfinal messages written to {out}")
