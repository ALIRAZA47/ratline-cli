import { Link } from 'react-router-dom';
import { CodeBlock } from '../components/CodeBlock';
import { PageHeader } from '../components/PageHeader';
import { Callout, Facts, H2 } from '../components/ui';
import { RefGroupHeading, RefList, RefRow } from '../components/Reference';

const fields: { name: string; type: string; when: string; meaning: string }[] = [
  { name: 'ok', type: 'bool', when: 'always', meaning: 'Whether the command succeeded. Branch on this.' },
  {
    name: 'command',
    type: 'string',
    when: 'always',
    meaning: 'The full command path, e.g. "ratline site list". Useful when several invocations are logged together.',
  },
  { name: 'version', type: 'string', when: 'always', meaning: 'The ratline version that produced the object.' },
  { name: 'data', type: 'object', when: 'on success', meaning: 'The command’s result. Shape varies by command.' },
  { name: 'error', type: 'object', when: 'on failure', meaning: 'The error payload, below.' },
  { name: 'error.code', type: 'int', when: 'on failure', meaning: 'The exit code, identical to the process exit status.' },
  { name: 'error.name', type: 'string', when: 'on failure', meaning: 'The stable machine-readable name: usage, precondition_failed, rate_limited…' },
  { name: 'error.message', type: 'string', when: 'on failure', meaning: 'What failed and why.' },
  { name: 'error.hint', type: 'string', when: 'when there is one', meaning: 'What to do next.' },
  { name: 'error.fields', type: 'object', when: 'when there are any', meaning: 'Structured details — the offending value, the observed DNS record, the lock holder.' },
];

export function JsonEnvelope() {
  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="The --json envelope"
        lede="Every --json invocation emits exactly one object on stdout. One shape for success and failure means a caller can branch on ok without special cases."
      />

      <CodeBlock
        lang="json"
        code={`{
  "ok": true,
  "command": "ratline site list",
  "version": "1.0.0",
  "data": {},
  "error": {
    "code": 3,
    "name": "precondition_failed",
    "message": "…",
    "hint": "…",
    "fields": {}
  }
}`}
        tag="both halves shown"
      />

      <Callout tone="note" title="data or error, never both">
        <p>
          The object above shows both keys so the whole surface is visible in one place.{' '}
          <code>data</code> is present on success, <code>error</code> on failure — the encoder omits
          whichever does not apply.
        </p>
      </Callout>

      {/* The envelope's fields as reference rows, each with its own anchor, so a caller
          arguing about `error.hint` can link to `error.hint` rather than to this page.
          The nested ones are indented under their parent the way an API reference nests
          an expandable object. */}
      <RefGroupHeading title="Fields" id="fields" />
      <RefList label="Fields of the JSON envelope">
        {fields.map((f) => (
          <RefRow
            key={f.name}
            anchor={`field-${f.name.replace(/\./g, '-')}`}
            name={f.name}
            type={f.type}
            indent={f.name.includes('.')}
            meta={[['present', f.when]]}
          >
            {f.meaning}
          </RefRow>
        ))}
      </RefList>

      <div className="prose">
        <H2 id="rules">Rules that hold for every command</H2>
      </div>

      <Facts
        rows={[
          ['stdout', 'Exactly one JSON object. Nothing else, ever.'],
          ['stderr', 'Logs, as JSON lines rather than prose — so machine consumers need not scrape.'],
          ['exit status', <>Identical to <code>error.code</code>. Both are the same contract.</>],
          ['secrets', 'Private key material never appears. Environment values are redacted unless --reveal.'],
          ['HTML escaping', 'Off, so a domain or a path is readable rather than entity-encoded.'],
          ['indentation', 'Two spaces. Pretty-printed on purpose: these objects end up in logs and issue reports.'],
          ['tables', <>Suppressed. A command that would print a table emits <code>data</code> instead.</>],
        ]}
      />

      <div className="prose">
        <H2 id="real">A real envelope</H2>
        <p>
          This is the actual output of <code>ratline version --json</code> on a machine with nothing
          installed. <code>os_supported: false</code> because it is macOS.
        </p>
      </div>

      <CodeBlock
        lang="json"
        code={`{
  "ok": true,
  "command": "ratline version",
  "version": "dev",
  "data": {
    "version": "dev",
    "commit": "none",
    "build_date": "unknown",
    "go": "go1.26.5",
    "platform": "darwin/arm64",
    "os": "darwin",
    "os_supported": false,
    "openssh": "10.2p1",
    "config": "/etc/ratline/config.yaml",
    "config_loaded": false
  }
}`}
      />

      <div className="prose">
        <p>
          And a failure. Note that <code>--json</code> and <code>--interactive</code> contradicting
          each other is still reported <em>inside</em> the envelope, because <code>--json</code> was
          asked for:
        </p>
      </div>

      <CodeBlock
        lang="json"
        code={`{
  "ok": false,
  "command": "ratline version",
  "version": "dev",
  "error": {
    "code": 2,
    "name": "usage",
    "message": "--json and --interactive contradict each other",
    "hint": "--json exists for automation, which cannot answer prompts"
  }
}`}
      />

      <div className="prose">
        <H2 id="parsing">Parsing it</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# The happy path
ratline site list --json | jq -r '.data.sites[] | "\\(.domain)\\t\\(.runtime)"'

# Errors: one field tells you what to do, the other tells you whether to retry
ratline cert issue example.com --json \\
  | jq -r 'if .ok then "ok" else "[\\(.error.name)] \\(.error.message) — \\(.error.hint)" end'

# Logs are on stderr, so redirecting them away leaves clean JSON
ratline export --json 2>/dev/null > state.json`}
      />

      <Callout tone="warn" title="One object, not a stream">
        <p>
          The envelope is a single object, not newline-delimited JSON. If you need a stream, the
          per-record output on <em>stderr</em> is JSON lines — that is where progress belongs, because
          stdout has to stay parseable as one document.
        </p>
      </Callout>

      <div className="prose">
        <p>
          See also: the <Link to="/reference/exit-codes">exit-code contract</Link>, and{' '}
          <Link to="/concepts/interactive">interactive mode</Link> for why{' '}
          <code>--json</code> and the wizard are mutually exclusive.
        </p>
      </div>
    </article>
  );
}
