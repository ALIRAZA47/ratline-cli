import type { CommandGroup } from '../types';

export const certs: CommandGroup = {
  id: 'cert',
  title: 'Certificates',
  path: '/reference/cert',
  blurb: 'TLS as an independently managed resource, not a flag on site add.',
  intro: [
    'TLS is a first-class resource with its own lifecycle. A site can be created and serving HTTP before DNS has propagated, then have a real certificate issued and attached later — which is the normal order of operations when a client is still moving their domain.',
    'Issuance is transactional and the verification step is real: after the reload, ratline opens a TLS connection with SNI and asserts that the served chain matches the expected fingerprint, covers the requested SANs and validates against the system root store. A certificate that exists on disk but is not being served is a failure, not a success.',
  ],
  commands: [
    {
      id: 'cert-issue',
      name: 'ratline cert issue',
      args: '<domain>',
      status: 'built',
      summary: 'Run preflight, obtain a certificate, attach it, and verify it is really being served.',
      description: [
        'Preflight runs first and reports every problem at once rather than one per attempt, because each attempt costs rate-limit budget. Only then is certbot invoked — as an argv slice, never a shell string.',
        'The transaction is: preflight → certbot → parse and translate the result → stage the vhost → nginx -t → reload → verify over TLS with SNI → record in state. Any failure restores the previous vhost and reloads.',
      ],
      flags: [
        {
          name: '--alias',
          arg: '<domain>',
          type: 'string',
          repeatable: true,
          default: 'the site’s aliases',
          description: 'Add a SAN. Defaults to whatever aliases the site already has.',
        },
        {
          name: '--san',
          arg: '<domain>',
          type: 'string',
          repeatable: true,
          description: 'An extra SAN that is not registered as a site alias.',
        },
        {
          name: '--challenge',
          arg: 'http|dns',
          type: 'enum',
          default: 'http',
          description: 'Validation method. HTTP-01 uses the shared webroot.',
          note: 'A wildcard request forces dns, and the refusal says why HTTP-01 cannot work: there is no single hostname to serve the token from.',
        },
        {
          name: '--dns-provider',
          arg: '<name>',
          type: 'string',
          description: 'DNS plugin to use for DNS-01, for example cloudflare, route53 or digitalocean.',
          note: 'Preflight checks the plugin is actually installed and prints the exact install command if it is not.',
        },
        {
          name: '--dns-credentials',
          arg: '<path>',
          type: 'path',
          description: 'Credentials file for the DNS plugin.',
          note: 'Must be 0600, and it is validated before use. Keep it under paths.dns_credentials (/etc/ratline/dns).',
        },
        {
          name: '--dns-propagation',
          arg: '<seconds>',
          type: 'int',
          default: '60',
          description: 'How long to wait for the DNS record to propagate before asking the CA to validate.',
        },
        {
          name: '--email',
          arg: '<address>',
          type: 'email',
          default: 'acme.email',
          description: 'ACME contact address.',
        },
        {
          name: '--staging',
          type: 'bool',
          default: 'false',
          description: 'Use Let’s Encrypt staging. Use it while testing.',
          note: 'Staging certificates are marked visibly untrusted in `cert list` so nobody ships one to production by accident. Staging has its own, far looser, rate limits — which is the whole point of testing there.',
        },
        {
          name: '--key-type',
          arg: 'ecdsa|rsa',
          type: 'enum',
          default: 'ecdsa (P-256)',
          description: 'Private key algorithm.',
        },
        {
          name: '--force',
          type: 'bool',
          default: 'false',
          description: 'Re-issue even if a valid certificate already exists, and proceed past a DNS mismatch.',
          note: 'It does not bypass the rate-limit budget, because that budget is what stops you locking yourself out of issuance for a week.',
        },
        {
          name: '--attach',
          type: 'bool',
          default: 'true',
          description: 'Point the vhost at the new certificate and reload nginx.',
        },
        {
          name: '--no-attach',
          type: 'bool',
          default: 'false',
          description: 'Obtain the certificate but leave the vhost alone.',
          note: 'Useful when you want to stage a cutover, or when the certificate is for a name the vhost does not serve yet.',
        },
        {
          name: '--dry-run',
          type: 'bool',
          default: 'false',
          description: 'Full validation with no rate-limit cost.',
          note: 'This is the flag to reach for first. It exercises preflight and the CA’s validation without spending a certificate.',
        },
      ],
      refuses: [
        'A DNS mismatch: A/AAAA records for the domain and every SAN are resolved, following CNAMEs, and compared against this server’s public addresses. The refusal prints the observed values. Override with --force only when you know why they differ.',
        'An attempt that would exceed a tracked CA rate limit — refused with a countdown, before anything is sent.',
        'A wildcard with --challenge http.',
        'Issuing when another vhost claims the same server_name, or when the site’s vhost is not enabled.',
        'Proceeding when the reachability probe fails: a random token is written to the shared webroot and fetched over HTTP, and only a 200 carrying the exact token passes.',
      ],
      exits: [
        { code: 2, reason: 'The domain, SAN list, email or credentials path failed validation.' },
        { code: 3, reason: 'The site does not exist, its vhost is not enabled, DNS does not point here, a server_name conflicts, or certbot or the DNS plugin is missing.' },
        { code: 4, reason: 'certbot itself failed for a reason other than validation.' },
        { code: 5, reason: 'Locked.' },
        { code: 6, reason: 'Attachment failed and the previous vhost could not be restored.' },
        { code: 8, reason: 'The ACME challenge failed. Distinct from 4 so automation can tell "certbot is broken" from "validation did not pass".' },
        { code: 9, reason: 'The attempt would exceed a CA rate limit. The message includes a retry-after.' },
      ],
      examples: [
        {
          title: 'The normal case, once DNS points here',
          lang: 'shell',
          code: 'ratline cert issue example.com --email admin@example.com',
        },
        {
          title: 'Check everything without spending an attempt',
          lang: 'shell',
          code: 'ratline cert issue example.com --dry-run',
        },
        {
          title: 'Apex plus www',
          lang: 'shell',
          code: 'ratline cert issue example.com --alias www.example.com',
        },
        {
          title: 'A wildcard, which must use DNS-01',
          lang: 'shell',
          code: `ratline cert issue "*.example.com" \\
  --challenge dns \\
  --dns-provider cloudflare \\
  --dns-credentials /etc/ratline/dns/cloudflare.ini \\
  --dns-propagation 90`,
        },
        {
          title: 'Behind a proxy that will not pass HTTP-01',
          lang: 'shell',
          code: `ratline cert issue example.com --challenge dns \\
  --dns-provider cloudflare \\
  --dns-credentials /etc/ratline/dns/cloudflare.ini`,
        },
      ],
      seeAlso: [
        { label: 'The TLS resource lifecycle', to: '/concepts/tls-lifecycle' },
        { label: 'HTTP-01 vs DNS-01', to: '/concepts/tls-lifecycle#challenge-decision' },
        { label: 'Rate limits', to: '/concepts/rate-limits' },
        { label: 'The Cloudflare orange-cloud trap', to: '/guides/cloudflare' },
      ],
      keywords: ['acme', 'letsencrypt', 'certbot', 'http-01', 'dns-01', 'san', 'wildcard', 'preflight'],
    },
    {
      id: 'cert-attach',
      name: 'ratline cert attach',
      args: '<domain>',
      status: 'built',
      summary: 'Point a site’s vhost at a certificate and verify it is served.',
      description: [
        'The other half of `--no-attach`, and the command to run after `cert import`. It stages the vhost, runs nginx -t, reloads, then opens a real TLS connection to confirm the served chain is the one you meant.',
      ],
      flags: [
        {
          name: '--cert',
          arg: '<name>',
          type: 'string',
          description: 'Which certificate to attach, when more than one could apply.',
        },
      ],
      exits: [
        { code: 3, reason: 'No such site or certificate, or the certificate does not cover the vhost’s names.' },
        { code: 4, reason: 'nginx -t or the reload failed; the previous vhost was restored.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline cert attach example.com' }],
    },
    {
      id: 'cert-detach',
      name: 'ratline cert detach',
      args: '<domain>',
      status: 'built',
      summary: 'Stop serving TLS for a site, leaving the certificate on disk.',
      exits: [
        { code: 3, reason: 'No such site, or nothing is attached.' },
        { code: 4, reason: 'nginx -t failed; the previous vhost was restored.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline cert detach example.com' }],
    },
    {
      id: 'cert-list',
      name: 'ratline cert list',
      status: 'built',
      summary: 'Every certificate on the box, including ones issued by hand outside ratline.',
      description: [
        'Columns: DOMAIN | SANS | ISSUER | KEY | EXPIRES | DAYS | STATUS | SITES | AUTO-RENEW.',
        'Certificates issued by hand outside ratline are detected and listed too — that is exactly the residue someone leaves behind, and a list that hid it would be worse than no list.',
      ],
      flags: [
        {
          name: '--expiring',
          arg: '<days>',
          type: 'int',
          description: 'Only certificates expiring within this many days.',
        },
        {
          name: '--orphaned',
          type: 'bool',
          default: 'false',
          description: 'Only certificates with no site attached.',
        },
        { name: '--json', type: 'bool', default: 'false', description: 'JSON envelope instead of a table.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline cert list --expiring 21' },
        { lang: 'shell', code: 'ratline cert list --orphaned' },
      ],
      seeAlso: [{ label: 'Certificate statuses explained', to: '/concepts/tls-lifecycle#statuses' }],
    },
    {
      id: 'cert-show',
      name: 'ratline cert show',
      args: '<domain>',
      status: 'built',
      summary: 'Subject, SANs, issuer, key type, dates, fingerprint, attached sites and renewal state.',
      examples: [{ lang: 'shell', code: 'ratline cert show example.com' }],
    },
    {
      id: 'cert-renew',
      name: 'ratline cert renew',
      args: '[<domain>]',
      status: 'built',
      summary: 'Renew one certificate, or every certificate inside the renewal window.',
      description: [
        'A timer runs twice daily with a randomised delay. certbot’s own timer is neutralised at install time so the two never race. Renewal is attempted under acme.renew_before_days (30) remaining.',
        'On failure: exponential backoff, the previous certificate is retained, the certificate is marked degraded in state, and `doctor` surfaces it. A failed renewal never leaves you with no certificate at all.',
      ],
      flags: [
        { name: '--all', type: 'bool', default: 'false', description: 'Every certificate inside the renewal window.' },
        {
          name: '--force',
          type: 'bool',
          default: 'false',
          description: 'Renew even if the certificate is not yet inside the window.',
          note: 'Each forced renewal counts against the duplicate-certificate limit, which is only 5 per week. This is the fastest way to lock yourself out of issuance for a domain.',
        },
        { name: '--dry-run', type: 'bool', default: 'false', description: 'Exercise renewal with no rate-limit cost.' },
      ],
      exits: [
        { code: 4, reason: 'certbot failed for a non-validation reason.' },
        { code: 8, reason: 'The challenge failed. The previous certificate is retained and the lineage is marked degraded.' },
        { code: 9, reason: 'The renewal would exceed a rate limit.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline cert renew --all --dry-run' },
        { lang: 'shell', code: 'ratline cert renew example.com' },
      ],
      seeAlso: [{ label: 'My cert didn’t renew', to: '/guides/renewal-runbook' }],
    },
    {
      id: 'cert-revoke',
      name: 'ratline cert revoke',
      args: '<domain>',
      status: 'built',
      summary: 'Ask the CA to revoke a certificate.',
      description: [
        'Revocation is for a compromised key, not for tidying up. If you simply want to stop using a certificate, `cert detach` and `cert delete` are the commands; revoking a certificate that is still being served makes the site untrusted for anyone whose client checks revocation.',
      ],
      flags: [
        {
          name: '--reason',
          arg: 'keycompromise|superseded|cessationofoperation',
          type: 'enum',
          description: 'The revocation reason recorded with the CA.',
        },
      ],
      exits: [
        { code: 3, reason: 'No such certificate.' },
        { code: 4, reason: 'certbot revoke failed.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline cert revoke example.com --reason keycompromise' },
      ],
    },
    {
      id: 'cert-delete',
      name: 'ratline cert delete',
      args: '<domain>',
      status: 'built',
      summary: 'Remove a certificate from state, and optionally from disk. Refuses while a site uses it.',
      flags: [
        {
          name: '--keep-files',
          type: 'bool',
          default: 'false',
          description: 'Drop it from state but leave the files where they are.',
        },
      ],
      refuses: ['Deleting a certificate that a site is still using. Detach it first.'],
      exits: [
        { code: 3, reason: 'A site still uses this certificate.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline cert detach example.com
ratline cert delete example.com` },
      ],
    },
    {
      id: 'cert-import',
      name: 'ratline cert import',
      args: '<domain>',
      status: 'built',
      summary: 'Bring in a certificate ratline did not issue — a commercial cert, or a proxy Origin certificate.',
      description: [
        'Imported certificates land under paths.imported_certs (/etc/ratline/certs). This is the answer when the box sits behind a proxy that terminates TLS itself and issues its own origin certificate: there is nothing for ACME to validate, so you import instead.',
      ],
      flags: [
        { name: '--cert', arg: '<file>', type: 'path', required: true, description: 'The full chain, PEM.' },
        { name: '--key', arg: '<file>', type: 'path', required: true, description: 'The private key, PEM.' },
        { name: '--chain', arg: '<file>', type: 'path', description: 'An intermediate chain, if it is separate.' },
      ],
      exits: [
        { code: 2, reason: 'A file is missing, unreadable, not PEM, or the key does not match the certificate.' },
        { code: 3, reason: 'The certificate does not cover the site’s names.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline cert import example.com \\
  --cert ./origin-fullchain.pem \\
  --key ./origin-privkey.pem
ratline cert attach example.com`,
        },
      ],
      seeAlso: [{ label: 'The Cloudflare orange-cloud trap', to: '/guides/cloudflare' }],
    },
    {
      id: 'cert-selfsign',
      name: 'ratline cert selfsign',
      args: '<domain>',
      status: 'built',
      summary: 'Generate a self-signed certificate so a vhost can serve HTTPS before DNS moves.',
      description: [
        'What `site add --ssl selfsigned` runs, and what the default falls back to when the domain does not yet resolve here. Self-signed certificates are marked as such in `cert list`, and HSTS is refused on them.',
      ],
      flags: [
        { name: '--days', arg: '<n>', type: 'int', default: '365', description: 'Validity period.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline cert selfsign example.com --days 90' }],
    },
    {
      id: 'cert-auto-renew',
      name: 'ratline cert auto-renew status|enable|disable',
      args: '[<domain>]',
      status: 'built',
      summary: 'Inspect or change automatic renewal, per certificate or globally.',
      description: [
        'With no domain, this operates on the renewal timer as a whole. With a domain, it changes that lineage only — which is what you want for a certificate you are deliberately letting lapse.',
      ],
      examples: [
        { lang: 'shell', code: `ratline cert auto-renew status
ratline cert auto-renew disable old.example.com` },
      ],
    },
    {
      id: 'cert-test-renewal',
      name: 'ratline cert test-renewal',
      status: 'built',
      summary: 'Prove the renewal path works, end to end, without spending anything.',
      description: [
        'Exercises the timer, the webroot, the deploy hook and the nginx reload. The thing that breaks renewal is almost never the CA — it is a webroot that stopped being reachable, a redirect that swallowed /.well-known, or a deploy hook that no longer maps a lineage to its sites. This is the command that catches all three before the certificate has 6 days left.',
      ],
      examples: [{ lang: 'shell', code: 'ratline cert test-renewal' }],
      seeAlso: [{ label: 'My cert didn’t renew', to: '/guides/renewal-runbook' }],
    },
    {
      id: 'cert-deploy-hook',
      name: 'ratline cert deploy-hook',
      status: 'built',
      summary: 'Called by certbot after a successful renewal; maps the lineage to its sites and reloads only those.',
      description: [
        'Not a command you normally run by hand. certbot invokes it, and it maps the renewed lineage to the sites using it, runs nginx -t, and reloads only the affected site. Never a blanket restart.',
      ],
      examples: [{ lang: 'shell', code: 'ratline cert deploy-hook' }],
    },
    {
      id: 'cert-account',
      name: 'ratline cert account show|register',
      status: 'built',
      summary: 'Inspect or create the ACME account.',
      flags: [
        {
          name: '--email',
          arg: '<address>',
          type: 'email',
          requiredWhen: 'register',
          description: 'The account contact address.',
        },
      ],
      description: [
        'acme.tos_agreed is set by `ratline init` once the operator has accepted the CA’s terms. ratline does not accept them on your behalf.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline cert account register --email admin@example.com' },
        { lang: 'shell', code: 'ratline cert account show' },
      ],
    },
  ],
};
