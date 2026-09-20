---
type: regex
pattern: '--command [''\"]?(/home/acme/api\.acme\.example/(app|venv)/|python)'
match: contains
weight: 2
---
The command is one a unit can resolve: a program on the site's own PATH (the venv's `python`), or an absolute path inside the site. A relative path cannot be resolved by a unit and is refused when the job is created.
