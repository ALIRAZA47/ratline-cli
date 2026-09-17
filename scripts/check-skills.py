#!/usr/bin/env python3
"""Check that every ratline command and flag named in the agent plugin exists in the binary.

The skills and agents under plugins/ratline are written for an AI agent to follow, and an
agent that reads `ratline site deploy --rollback` in a skill will run it as root on
somebody's server. So a flag that a release renamed, or that never existed, is not a typo
in a document: it is a wrong command with a confident source. This is the same reasoning
that has docs/reference/commands.md generated from the binary and diffed in CI, applied to
prose that cannot be generated.

    python3 scripts/check-skills.py ./bin/ratline [plugins/ratline]

It reads `ratline schema` for the command tree, then walks every fenced code block and
inline code span in the plugin's markdown (and its YAML templates), finds each `ratline …`
invocation, resolves the longest command path it names, and checks every `--flag` after it
against that command's flags, its ancestors' persistent flags and the global flags.
Placeholders like `<domain>`, `…` and `$VAR` end path matching without complaint; an
unknown subcommand or flag is a failure with the file and line.
"""
import json
import pathlib
import re
import shlex
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

TERMINATORS = {"|", "||", "&&", ";", ">", ">>", "<", "2>&1", "2>", "&"}
# Tokens that stand for "something goes here" rather than a literal word.
PLACEHOLDER = re.compile(r"[<>…\[\]{}$*]|^X$|^\.\.\.$")
# Where an invocation can start: bare, absolute, or after sudo / ssh … sudo.
COMMENTED_COMMAND = re.compile(r"^#+\s*(sudo\s+)?(/usr/local/bin/|\./bin/)?ratline\s")
RATLINE_NAMES = {"ratline", "/usr/local/bin/ratline", "./bin/ratline", "bin/ratline"}
# What may immediately precede a ratline invocation. Anything else means the token is an
# argument to another program rather than the start of a command.
COMMAND_POSITION = {"sudo", "-T", "command", "exec", "time", "watch", "then", "do", "else",
                    "&&", "||", ";", "|", "(", "{", "$(", "`"}


def load_schema(binary: str) -> dict:
    out = subprocess.run([binary, "schema"], check=True, capture_output=True, text=True).stdout
    return json.loads(out)


class Surface:
    """The command tree flattened into lookups."""

    def __init__(self, schema: dict):
        self.paths: dict[str, dict] = {}
        self.globals = {f["name"]: f.get("type", "bool") for f in schema.get("global_flags", [])}
        self.globals.setdefault("help", "bool")

        def walk(cmds):
            for c in cmds:
                self.paths[c["path"]] = c
                walk(c.get("subcommands", []))

        walk(schema["commands"])
        self.paths["ratline"] = {"path": "ratline", "subcommands": schema["commands"], "flags": []}

    def flags_for(self, path: str) -> dict[str, str]:
        """A command's flags plus every ancestor's, because cobra persistent flags
        (`db --engine`) are declared on the group and accepted by its children."""
        flags = dict(self.globals)
        parts = path.split()
        for i in range(1, len(parts) + 1):
            cmd = self.paths.get(" ".join(parts[:i]))
            if cmd:
                for f in cmd.get("flags", []):
                    flags[f["name"]] = f.get("type", "bool")
                    if f.get("short"):
                        flags[f["short"]] = f.get("type", "bool")
        return flags

    def is_group(self, path: str) -> bool:
        return bool(self.paths.get(path, {}).get("subcommands"))

    def takes_args(self, path: str) -> bool:
        return bool(self.paths.get(path, {}).get("args"))


def strip_quotes(tok: str) -> str:
    return tok.strip("'\"`")


def split_line(line: str) -> list[str]:
    try:
        return shlex.split(line, comments=True, posix=True)
    except ValueError:
        return line.replace("`", " ").split()


def embedded(tok: str) -> bool:
    """Is this one token a whole ratline command line?

    `--command '/usr/local/bin/ratline site deploy app --install --json'` is the pinned
    argv a sudoers rule compares against, and `ssh host "ratline …"` is a remote command.
    Both arrive as a single token, and both are commands that will really run as root, so
    a wrong flag inside one is the most expensive kind there is.
    """
    return " " in tok and any(tok.startswith(n + " ") for n in RATLINE_NAMES)


def check_tokens(tokens: list[str], surface: Surface, where: str, problems: list[str]) -> None:
    """Find every ratline invocation in one token list and validate it."""
    i = 0
    while i < len(tokens):
        tok = strip_quotes(tokens[i])
        # A pinned sudoers argv or a quoted remote command carries a whole invocation in one
        # token. Check it as its own line: that is exactly the string sudo will compare.
        if embedded(tok):
            check_tokens(split_line(tok), surface, where, problems)
            i += 1
            continue
        if tok not in RATLINE_NAMES:
            i += 1
            continue
        # Only a token in *command position* starts an invocation. `python3 analyze.py
        # ./bin/ratline .` passes the binary as an argument to something else, and reading
        # the next word as a subcommand invents a failure.
        if i > 0:
            prev = strip_quotes(tokens[i - 1])
            if prev not in COMMAND_POSITION and prev not in TERMINATORS:
                i += 1
                continue

        path = "ratline"
        j = i + 1
        pending_flags: list[str] = []
        flags_unknown = False
        # Resolve the longest command path, letting flags sit between a group and its
        # subcommand (`ratline db --engine mysql connect`) and stopping at a placeholder.
        while j < len(tokens):
            t = strip_quotes(tokens[j])
            if t in TERMINATORS:
                break
            if t.startswith("-"):
                pending_flags.append(t)
                name = t.lstrip("-").split("=", 1)[0]
                ftype = surface.flags_for(path).get(name)
                if ftype and ftype != "bool" and "=" not in t and j + 1 < len(tokens):
                    j += 1  # the flag's value
                    value = strip_quotes(tokens[j])
                    if embedded(value):
                        check_tokens(split_line(value), surface, where, problems)
                j += 1
                continue
            if PLACEHOLDER.search(t):
                j += 1
                if surface.is_group(path):
                    # `ratline new <runtime> --with-db`: the flags belong to a subcommand
                    # the placeholder stands for, so there is nothing sound to check.
                    flags_unknown = True
                break
            candidate = f"{path} {t}"
            if candidate in surface.paths:
                path = candidate
                j += 1
                continue
            if surface.is_group(path):
                problems.append(f"{where}: `{path}` has no subcommand `{t}`")
            elif not surface.takes_args(path):
                problems.append(f"{where}: `{path}` takes no argument, but `{t}` follows it")
            j += 1
            break

        # Everything up to the next terminator is arguments and flags of `path`.
        flags = surface.flags_for(path)
        rest = pending_flags[:]
        while j < len(tokens):
            t = strip_quotes(tokens[j])
            if t in TERMINATORS:
                break
            if embedded(t):
                check_tokens(split_line(t), surface, where, problems)
                j += 1
                continue
            if t.startswith("-") and len(t) > 1 and not PLACEHOLDER.search(t):
                rest.append(t)
                name = t.lstrip("-").split("=", 1)[0]
                ftype = flags.get(name)
                if ftype and ftype != "bool" and "=" not in t and j + 1 < len(tokens):
                    j += 1
                    value = strip_quotes(tokens[j])
                    if embedded(value):
                        check_tokens(split_line(value), surface, where, problems)
            j += 1
        for f in rest:
            if flags_unknown or PLACEHOLDER.search(f) or f == "--":
                continue
            name = f.lstrip("-").split("=", 1)[0]
            if name not in flags:
                problems.append(f"{where}: `{path}` has no flag `--{name}`")
        i = j


def code_lines(text: str):
    """Yield (line_number, line) for fenced code blocks and inline code spans, with
    backslash continuations joined so a multi-line command is one command."""
    lines = text.split("\n")
    in_fence = False
    buf, buf_start = "", 0
    for n, raw in enumerate(lines, 1):
        stripped = raw.strip()
        if stripped.startswith("```") or stripped.startswith("~~~"):
            in_fence = not in_fence
            if buf:
                yield buf_start, buf
                buf = ""
            continue
        if in_fence:
            if raw.rstrip().endswith("\\"):
                if not buf:
                    buf_start = n
                buf += raw.rstrip()[:-1] + " "
                continue
            if buf:
                yield buf_start, buf + raw
                buf = ""
            else:
                yield n, raw
        else:
            for span in re.findall(r"`([^`\n]+)`", raw):
                yield n, span
    if buf:
        yield buf_start, buf


def check_file(path: pathlib.Path, surface: Surface, problems: list[str]) -> int:
    text = path.read_text()
    try:
        rel = path.relative_to(ROOT)
    except ValueError:
        rel = path
    count = 0
    if path.suffix in (".yml", ".yaml"):
        # A workflow template: every line is code.
        lines = [(n, l) for n, l in enumerate(text.split("\n"), 1)]
    else:
        lines = list(code_lines(text))
    for n, line in lines:
        if "ratline" not in line:
            continue
        stripped = line.strip()
        if path.suffix in (".yml", ".yaml") and stripped.startswith("#"):
            # A comment in a template is prose unless it is showing a command: keep the
            # ones that start with ratline (or sudo ratline, or the absolute path) and
            # drop the sentences, which shlex would otherwise read as arguments.
            if not COMMENTED_COMMAND.match(stripped):
                continue
            line = stripped.lstrip("#").strip()
        tokens = split_line(line)
        before = len(problems)
        check_tokens(tokens, surface, f"{rel}:{n}", problems)
        count += 1
    return count


def main() -> int:
    binary = sys.argv[1] if len(sys.argv) > 1 else str(ROOT / "bin/ratline")
    target = (pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else ROOT / "plugins/ratline").resolve()
    if not pathlib.Path(binary).exists():
        print(f"no binary at {binary}; run `make build` first", file=sys.stderr)
        return 2
    surface = Surface(load_schema(binary))

    files = sorted(
        p for p in target.rglob("*")
        if p.suffix in (".md", ".yml", ".yaml") and p.is_file()
    )
    problems: list[str] = []
    checked = 0
    for f in files:
        checked += check_file(f, surface, problems)

    print(f"{checked} ratline invocation(s) checked across {len(files)} file(s) "
          f"against {len(surface.paths)} command path(s)")
    if not problems:
        print("ok: every command and flag the plugin names exists in this binary")
        return 0
    print("\n✗ these do not match the binary:\n", file=sys.stderr)
    for p in problems:
        print(f"  {p}", file=sys.stderr)
    print("\nFix the skill, or, if the binary is wrong, the binary — never by making the check "
          "looser.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
