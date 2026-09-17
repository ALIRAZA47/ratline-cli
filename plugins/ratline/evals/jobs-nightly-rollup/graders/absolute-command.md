---
type: regex
pattern: '--command [''\"]?/home/acme/api\.acme\.example/(app|venv)/'
match: contains
weight: 2
---
The command is an absolute path inside the site (the venv's python and the script), because there is no shell PATH.
