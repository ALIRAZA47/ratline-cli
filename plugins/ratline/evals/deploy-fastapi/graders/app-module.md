---
type: regex
pattern: '--app-module app\.main:app'
match: contains
weight: 2
---
The import path is app.main:app (app/main.py defines `app`), not main:app and not a file path.
