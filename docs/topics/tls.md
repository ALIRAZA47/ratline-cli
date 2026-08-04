# Certificates

> TLS is a separate resource, on purpose.

`ratline cert` manages certificates independently of sites, which is why a site can
be created and serving over HTTP before DNS has been pointed at the server. A
certificate is attached to a vhost afterwards.

    ratline site add app.example.com --user acme --runtime node --entry server.js
    # ... point DNS at this server ...
    ratline cert issue app.example.com --email admin@example.com

## Before it asks the CA for anything

Let's Encrypt's rate limits are counted against your domain whether an attempt was
sensible or not, so ratline checks locally first:

* the name resolves, and resolves **here** — including the case of a proxy in front,
  where it reports the provider rather than claiming the DNS is wrong;
* port 80 is reachable from outside, for an HTTP-01 challenge;
* no other certificate already covers the name;
* certbot and the DNS plugin are present;
* the local record of recent attempts leaves budget under the limit.

A refusal names which check failed and what to do. `--staging` uses the staging CA,
which has far looser limits and is the right way to debug an issuance problem.

## Verification is a real handshake

After issuance, ratline connects to the site over TLS and checks the certificate
that is actually served. A certificate on disk that nginx never loaded is a
distinction that matters, and only a handshake can tell the difference.

## Renewal

A systemd timer renews anything within `renew_before_days` of expiry, reloads nginx
through the deploy hook, and records the outcome. `ratline cert list` shows days
remaining; consecutive failures are counted, so a certificate that has been failing
quietly for a week is visible rather than a surprise at expiry.

    ratline cert list
    ratline cert renew app.example.com --force
    ratline cert show app.example.com

## Bringing your own

    ratline cert import app.example.com --cert fullchain.pem --key privkey.pem

The key is validated against the certificate, stored `0600` outside any document
root, and never appears in logs, errors or `--json` output.

## HSTS

Off by default, and ratline refuses to enable it on a self-signed or staging
certificate. Enabling HSTS for one host can break a tenant's unrelated subdomains
for as long as the max-age, and that is not a decision a provisioning tool should
make on your behalf.

See also: `ratline explain diagnose`.
