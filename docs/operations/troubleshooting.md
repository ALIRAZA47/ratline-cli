# Troubleshooting

Start here:

```bash
sudo ratline site troubleshoot app.example.com
```

It follows a request the way a request travels — nginx configuration, then the
directories, then the unit, then the process manager, then the socket, then the
application, then nginx end to end, then TLS, then DNS — and stops at the first
failure. Checking in that order means the first failure **is** the cause; everything
after it is a consequence not worth reporting separately.

```
app.example.com

  ok    enabled
  ok    nginx configuration  —  /etc/nginx/sites-available/app.example.com.conf
  ok    nginx accepts the configuration
  ok    site directory  —  /home/acme/app.example.com
  ok    systemd unit  —  active, pid 41822
  ok    pm2 workers  —  4 online
  FAIL  socket permissions  —  the socket is mode 0640; nginx needs 0660 to connect,
        so every request is a 502
  --    the application answers  —  an earlier step has to pass first

Likely cause: the socket is mode 0640; nginx needs 0660 to connect, so every
              request is a 502
Try:          ratline site restart app.example.com; the full story is in
              'ratline explain sockets'
```

## 502 Bad Gateway

nginx is running and cannot reach your application. In likelihood order:

1. **The service is not running.** `ratline site status <domain>`, then
   `ratline site logs <domain>` — on a PM2 site add `--journal` to see whether the unit
   itself failed to start.
2. **Socket permissions.** The one that produces an *empty* application log, because no
   request ever arrives: `connect(2)` needs write permission on the socket inode, and at
   `0640` nginx gets `EACCES`. `ratline explain sockets`.
3. **The application crashed after starting.** `ratline site status` shows PM2's restart
   count; systemd's stays at zero on a PM2 site.
4. **It is listening somewhere else.** A framework that ignores `PORT` and hardcodes
   `3000` starts cleanly and answers nothing.

## 404 on a path that should exist

Static SPA: `--spa` is missing, so unmatched paths do not fall back to the index
document. `ratline site show <domain>` shows whether it is set.

Proxied site: check `/etc/nginx/ratline/custom/<domain>.conf`. It is included by the
generated vhost and never regenerated, so a stale rule there survives everything else.

## 413 Request Entity Too Large

```bash
sudo ratline site scale app.example.com --client-max-body-size 100M
```

## The site starts, then stops

Usually a hardening directive. Each one is verified at install time, so the failure
names the directive:

```bash
sudo ratline site runtime app.example.com --relax ProtectHome
```

`journalctl -u ratline-<slug> -n 50` has the rest.

## The certificate is not being served

A certificate on disk that nginx never loaded passes every check except a handshake.
`ratline cert show <domain>` connects and reports what is actually served.

## Nothing above matched

```bash
sudo ratline doctor                        # every check, across the server
sudo ratline status                        # the inventory
sudo ratline site show <domain>            # every setting for this site
sudo journalctl -u ratline-<slug> -n 50    # the unit's own messages
sudo nginx -t                              # the configuration nginx sees
```

Concept pages, readable on the server: `ratline explain diagnose`,
`ratline explain sockets`, `ratline explain node`.
