---
type: llm
weight: 2
---
Judge the plan in the final message against these criteria; quote the evidence for each, and pass only if all hold. (Whether each flag exists is checked separately by a script; do not judge that here.)
1. The runtime is node (via `ratline new node …` or `ratline site add … --runtime node`), the tenant is acme, and the domain is shop.acme.example.
2. Build-time variables (NEXT_PUBLIC_STRIPE_PUBLISHABLE_KEY, NEXT_PUBLIC_SITE_URL) are set BEFORE the first deploy/build, and STRIPE_SECRET_KEY / AUTH_SECRET are treated as secrets (stdin or an interactive prompt, never a literal value in a command).
3. The plan asks the user for what it cannot know (the SSH public key to authorise, the certificate email, confirmation of DNS) rather than inventing values.
4. Success is verified with a real request or `site health` / `troubleshoot`, not assumed from the unit being active.
