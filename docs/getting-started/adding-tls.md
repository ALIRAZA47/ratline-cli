# Adding TLS

A site is created and serving over HTTP before DNS points anywhere, because
certificates are a separate resource with their own lifecycle. Once DNS is pointed
at this server:

```bash
sudo ratline cert issue app.example.com --email admin@example.com
```

## What happens before the CA is contacted

Let's Encrypt's rate limits count failed attempts against your domain, so ratline
checks locally first and refuses with a reason rather than burning budget:

* the name resolves, and resolves to an address of this server — including
  recognising a proxy in front and naming the provider rather than claiming your DNS
  is wrong;
* port 80 is reachable from outside, for the HTTP-01 challenge;
* no existing certificate already covers the name;
* certbot and any needed DNS plugin are installed;
* the local record of recent attempts leaves budget under the published limits.

## While debugging, use staging

```bash
sudo ratline cert issue app.example.com --staging --email admin@example.com
```

The staging CA's limits are far looser. A staging certificate is not trusted by
browsers, and ratline knows that — it refuses to enable HSTS on one.

## Wildcards need DNS-01

```bash
sudo ratline cert issue '*.example.com' --challenge dns --dns-provider cloudflare
```

DNS credentials go in `/etc/ratline/dns`, `0600`, and never in argv.

## Verification is a handshake

After issuance ratline connects to the site over TLS and inspects the certificate
that is actually served. A certificate that exists on disk but was never loaded by
nginx looks fine in every other check.

## Renewal

A systemd timer handles it. `ratline cert list` shows days remaining and counts
consecutive failures, so a certificate that has been quietly failing for a week is
visible rather than a surprise at expiry.

```bash
sudo ratline cert list
sudo ratline cert renew app.example.com --force
```

## Your own certificate

```bash
sudo ratline cert import app.example.com --cert fullchain.pem --key privkey.pem
```

The key is checked against the certificate, stored `0600` outside any document root,
and never appears in a log, an error or `--json` output.

More detail: [security/tls.md](../security/tls.md), or `ratline explain tls`.

Next: [giving-access.md](giving-access.md).
