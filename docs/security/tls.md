# TLS

Certificates are a resource with their own lifecycle, not a flag on `site add`.
That is deliberate: the normal order of operations when a client is still moving
their domain is *site first, DNS later, certificate last*, and a tool that demands
working DNS before it will create a site makes that impossible.

## The model

```bash
# 1. the site exists and serves HTTP, before DNS points anywhere
ratline site add example.com --user acme --runtime static --ssl none

# 2. the client points their DNS at you. later that day:
ratline cert issue example.com --email admin@example.com

# 3. done. HTTPS, HTTP redirects to it, renewal is on a timer
```

`cert issue` does the ACME exchange and attaches the result. `cert attach` does the
attaching alone, which is how one SAN certificate serves several vhosts.

## Before an attempt is spent

`cert issue` runs every preflight check first and reports **all** the problems at
once, because fixing one per attempt is a poor way to spend a rate-limit budget:

1. The site exists and its vhost is enabled.
2. **DNS** — A and AAAA records for the domain and every SAN are resolved,
   following CNAMEs, and compared against this server's public addresses. A mismatch
   is refused with the observed values.
3. **Proxy detection** — if the resolved address is in a known proxy range, HTTP-01
   cannot work until the record is DNS-only. You are told that, and pointed at
   `--challenge dns` or an Origin certificate.
4. **Reachability** — a random token is written to the shared webroot and fetched
   over HTTP. A 200 with the exact token is the only pass.
5. **Conflicts** — no other vhost claims the same `server_name`.
6. **Tooling** — certbot, and the DNS plugin if you asked for one, with the exact
   install command if either is missing.
7. **Rate-limit budget** — below.
8. A wildcard forces `--challenge dns`, and says why HTTP-01 cannot do it.

## HTTP-01 or DNS-01

```
Do you need a wildcard (*.example.com)?
├── yes ─────────────────────────────► DNS-01. There is no other option.
└── no
    │
    Is the domain behind a proxy (Cloudflare orange cloud, Fastly, …)?
    ├── yes ──► either grey-cloud the record, or DNS-01, or import an Origin cert
    └── no
        │
        Is port 80 reachable from the internet?
        ├── yes ─────────────────────► HTTP-01. The default. Nothing to configure.
        └── no ──────────────────────► open port 80, or DNS-01
```

HTTP-01 is the default because it needs no credentials and no propagation wait.
DNS-01 needs an API token for your provider, stored `0600` under
`/etc/ratline/dns/`, and a propagation wait that is genuinely required — validating
before the TXT record has propagated is a failed attempt against your budget.

### A provider certbot has no plugin for

certbot ships plugins for around a dozen providers. For everything else — and for a
company's internal DNS — `--dns-provider manual` takes a script instead of a credentials
file:

    ratline cert issue '*.example.com' \
        --dns-provider manual \
        --dns-hook /etc/ratline/dns/publish.sh \
        --dns-cleanup-hook /etc/ratline/dns/withdraw.sh

certbot sets `CERTBOT_DOMAIN` and `CERTBOT_VALIDATION`; the script publishes a TXT record
at `_acme-challenge.$CERTBOT_DOMAIN` however your provider requires. For a wildcard the
domain arrives with the `*.` already stripped, so one script handles both cases.

**The hook runs as root**, with the validation token in its environment, on a server
holding every tenant's keys. Anyone who can write it can run code as root — so ratline
refuses a hook that is not an absolute path, not owned by root, not executable, or
writable by group or other. That check runs in preflight too, because discovering it after
certbot has started costs an attempt against the rate limit.

The cleanup hook is optional but worth having: without it the TXT records accumulate, and
some providers rate-limit record creation.

## The Cloudflare orange-cloud trap

This is the most common certificate failure on a new server, and it looks like a
ratline bug.

With Cloudflare's proxy on (the orange cloud), the A record resolves to a
Cloudflare address, not yours. HTTP-01 sends the challenge request to Cloudflare,
which forwards it to your server over HTTPS — and if your certificate is not valid
yet, that fails. The preflight detects the proxy range and tells you so rather than
letting certbot fail with something opaque.

Three ways out:

1. **Grey-cloud the record**, issue the certificate, then turn the proxy back on.
   Simplest, and works every time.
2. **`--challenge dns`** with a Cloudflare API token. Works with the proxy on.
3. **A Cloudflare Origin certificate**, which lasts fifteen years and is trusted by
   Cloudflare specifically:
   ```bash
   ratline cert import example.com --cert origin.pem --key origin.key
   ```
   Note that nothing renews an imported certificate. `doctor` warns as it approaches
   expiry, but there is no automation to save you.

## Rate limits

Let's Encrypt's limits are per registered domain and unforgiving. ratline records
every attempt — successes *and* failures, since the CA counts failed validations
too — and computes the remaining budget before acting. An attempt that would exceed
it is refused with a countdown, rather than discovered.

Defaults, all configurable because they are CA policy and do change:

| Limit | Default |
|---|---|
| Certificates per registered domain per week | 50 |
| Duplicate certificates (the same SAN set) per week | 5 |
| Failed validations per hostname per hour | 5 |
| New orders per account per 3 hours | 300 |

While you are testing, use one of these — neither costs anything against the
production budget:

```bash
ratline cert issue example.com --dry-run    # full validation, no certificate
ratline cert issue example.com --staging    # a real but untrusted certificate
```

Staging certificates are marked as untrusted in `cert list`, so nobody ships one to
production by accident. ratline also refuses to enable HSTS on a staging or
self-signed certificate: a browser that has seen HSTS refuses plain HTTP
afterwards, and pinning it to a certificate it cannot verify would lock the site out
of its own domain.

## Issue is transactional

Preflight, then certbot (an argv slice, never a shell string), then the result is
parsed and translated — a rate limit becomes a countdown, a DNS mismatch becomes
observed-versus-expected, a connection refused becomes a firewall hint. Then the
vhost is staged, `nginx -t` runs, and nginx reloads.

Then it is **verified for real**: a TLS connection is opened to the site over the
public interface with SNI set, and the served chain is checked against the expected
fingerprint, the requested SANs, and the system root store. A certificate that
exists on disk but is not being served is a failure, not a success.

Any failure at any step restores the previous vhost and reloads it.

## Renewal

A timer runs twice daily with a randomised delay, calling
`ratline cert renew --all`. Renewal is attempted under 30 days remaining, which
leaves four weeks of slack for a transient failure.

certbot's own timer is disabled at install time, and the installer says so. Two
timers would race, each reloading nginx from under the other.

certbot calls back into `ratline cert deploy-hook`, which maps the renewed lineage
to its sites through state, runs `nginx -t`, and reloads **only** the affected
site. Never a blanket restart of everything.

On failure: exponential backoff, the previous certificate is kept (it is still
valid for weeks), the certificate is marked `degraded` in state, and `doctor`
reports it. Optionally a webhook or email fires.

Find breakage weeks before expiry:

```bash
ratline cert test-renewal      # dry-runs every certificate
```

Worth a monthly cron.

## `cert list`

```
DOMAIN           SANS                        ISSUER  KEY    EXPIRES     DAYS  STATUS   SITES  AUTO-RENEW
example.com      example.com,www.example.com R11     ecdsa  2026-11-02   61   valid    2      yes
api.example.com  api.example.com             R11     ecdsa  2026-08-19   15   expiring 1      yes
old.example.com  old.example.com             R10     rsa    2026-07-01  -34   expired  0      no
```

`orphaned` means no site is attached. `unattached-mismatch` means the certificate
exists but the vhost points elsewhere. `degraded` means the last renewal failed.

Certificates issued by hand with certbot, outside ratline, are detected and listed
too — that is exactly the residue someone leaves behind, and pretending it is not
there does not help.

## My certificate did not renew

**1. Ask.**

```bash
ratline cert show example.com     # includes the renewal log tail
ratline doctor
journalctl -u ratline-cert-renew.service -n 50
```

**2. Try it, without spending an attempt.**

```bash
ratline cert renew example.com --dry-run
```

**3. The usual causes, in order of how often they are it.**

**Port 80 is closed.** The most common by a distance. Someone tightened a firewall
or moved the site to HTTPS-only and closed 80. The ACME challenge lives there.

```bash
curl -I http://example.com/.well-known/acme-challenge/test
```

Should be a 404 from nginx, not a timeout. A timeout is a firewall.

**DNS moved.** The domain now points somewhere else. `cert show` lists the observed
addresses against yours.

**The proxy was turned on.** See the orange-cloud section.

**A redirect swallowed the challenge.** ratline includes the challenge location
before every redirect, in every vhost, even for a disabled site — but a hand-written
rule in `/etc/nginx/ratline/custom/<domain>.conf` can still shadow it. Check that
file.

**The site is disabled.** ratline keeps a disabled site answering the challenge on
purpose, which is why a paused site can still renew. If you removed the vhost by
hand instead of using `ratline site disable`, that protection is gone.

**4. Force it once the cause is fixed.**

```bash
ratline cert renew example.com --force
```

## Importing a certificate

```bash
ratline cert import example.com --cert fullchain.pem --key privkey.pem
```

Validated before anything is installed: the PEM parses, the private key matches the
certificate's public key, the chain is in the right order and builds to a trusted
root (a private CA warns rather than fails), `notAfter` is in the future, the SANs
cover the domain and its aliases, and the key is not passphrase-encrypted — with the
exact `openssl` command to decrypt it if it is.

Imported certificates have no automatic renewal. They are marked as such, and
`doctor` warns as expiry approaches, because nothing else will.

## Self-signed placeholders

```bash
ratline cert selfsign example.com
```

So a site can serve HTTPS the moment it is created, before DNS is pointed. Recorded
distinctly, never counted as valid, always flagged in `cert list` and `doctor`, and
replaced cleanly by `cert issue` later. HSTS is refused on one.

## Where things live

```
/etc/letsencrypt/live/<lineage>/     certbot's; ratline reads, never edits
/etc/ratline/certs/<domain>/         imported certificates, 0700 root:root
    ├── fullchain.pem                0644
    ├── privkey.pem                  0600 root:root — never inside a home
    └── meta.json                    source, imported_at, fingerprint
/var/www/ratline-acme/               one shared HTTP-01 webroot for every site
/etc/ratline/dns/<provider>.ini      DNS-01 credentials, 0600 root:root
```

Private keys are never world-readable, never logged, never included in `--json`
output, and never placed anywhere nginx could serve them. Fingerprints and serials
are safe to display and are what ratline shows.
