---
type: llm
weight: 2
---
Quote the evidence for each item; pass only if the answer: (1) chooses site scope and explains why (file transfer only, no shell, confined to the site directory, not a kernel boundary); (2) uses --from, --expires and --label; (3) shows `ratline key test <label>` and says to read it against the intent before handing the key over; (4) tells the designer how to connect (sftp/rsync as the acme account to the site directory) and how to remove or revoke later; (5) the plan rehearses the grant with `--dry-run` before running it. Flag validity is checked separately by a script, not here.
