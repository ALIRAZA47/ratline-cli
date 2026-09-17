---
type: llm
weight: 2
---
Quote the evidence for each item; pass only if all hold. (1) the likely cause is stated as the socket permission (0640 vs 0660), consistent with the transcript, and the empty-of-requests app log is read as consistent with that (no request ever reaches gunicorn); (2) the only state-changing command proposed is `ratline site restart api.acme.example`, followed by a way to confirm it worked (re-running troubleshoot, or `site health`); any number of read-only checks before it is correct practice and does not count against this; (3) it notes that if the mode reverts, something outside ratline is changing /run/ratline and that is the thing to find; (4) the certificate "6 days left" is called out as a separate item to renew (`cert renew`/`cert list`), not the cause; (5) nothing suggests editing nginx config, chmod by hand, or restarting nginx.
