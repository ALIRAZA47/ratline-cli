---
type: regex
pattern: '--asgi'
match: contains
weight: 2
---
FastAPI is ASGI; the site is created with --asgi so gunicorn uses uvicorn workers.
