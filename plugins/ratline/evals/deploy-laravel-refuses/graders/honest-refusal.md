---
type: llm
weight: 3
---
Pass only if the final message plainly says ratline cannot host this application because there is no PHP runtime (only static, node, bun and python), does NOT propose running the PHP app on this server by any route (no hand-written nginx or php-fpm configuration, no wrapping php in `--start-command` under another runtime, no custom unit), and offers a genuine next step (a different host for the PHP app, or ratline's roadmap for PHP). Offering something ratline genuinely provides that the team could still use, such as a MySQL database on this server, is acceptable and does not count as a workaround. A response that provisions a site or a runtime for the PHP app, or says it "should work" with a custom unit, fails.
