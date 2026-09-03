package rl

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Client runs the ratline binary.
type Client struct {
	// Binary is the absolute path to ratline. Verified root-owned and not
	// group-writable before the first invocation: the panel runs it as root, so
	// anybody who can rewrite it can become root, and a panel that would execute a
	// tenant-writable binary has undone the tool's own rule about that.
	Binary string
	// ConfigPath, when set, is passed as --config. Normally empty: ratline finds
	// /etc/ratline/config.yaml on its own and the panel has no business overriding
	// where a server's configuration lives.
	ConfigPath string

	Runner system.Runner
	Log    *log.Logger

	// ReadTimeout bounds a listing or a show.
	ReadTimeout time.Duration
	// WriteTimeout bounds a mutation that runs inside a request.
	WriteTimeout time.Duration
	// JobTimeout bounds a long job: a deploy that clones, installs and builds, or
	// a runtime compiled from source.
	JobTimeout time.Duration

	cache schemaCache
}

// Defaults chosen from what these actually take on a small VPS: a listing is a
// process spawn and a SQLite read; a deploy is npm and a build.
const (
	DefaultReadTimeout  = 45 * time.Second
	DefaultWriteTimeout = 5 * time.Minute
	DefaultJobTimeout   = 45 * time.Minute
	// How long a cached command surface is trusted. Long enough that the panel is
	// not forking a process to answer "what flags does this take", short enough
	// that `ratline update` in another terminal is picked up without a restart.
	schemaTTL = 5 * time.Minute
)

// NewClient returns a client, checking the binary before it is ever run.
func NewClient(binary string, runner system.Runner, lg *log.Logger) (*Client, error) {
	if err := system.CheckRootOwnedExecutable(binary); err != nil {
		return nil, rlerr.Preconditionf("the ratline binary at %s cannot be used: %s",
			binary, err.Error()).
			WithHint("the panel runs it as root; it must be root-owned and executable")
	}
	if lg == nil {
		lg = log.Discard()
	}
	c := &Client{
		Binary:       binary,
		Runner:       runner,
		Log:          lg,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		JobTimeout:   DefaultJobTimeout,
	}
	c.cache.ttl = schemaTTL
	return c, nil
}

// Catalogue returns the installed binary's command surface, cached.
func (c *Client) Catalogue(ctx context.Context) (*Catalogue, error) {
	return c.cache.get(ctx, c.loadCatalogue)
}

// InvalidateCatalogue forces the next read to re-run `ratline schema`. Called after
// the panel itself runs `update`, because that is the one moment the surface is known
// to have changed.
func (c *Client) InvalidateCatalogue() { c.cache.invalidate() }

func (c *Client) loadCatalogue(ctx context.Context) (*Catalogue, error) {
	ctx, cancel := context.WithTimeout(ctx, c.ReadTimeout)
	defer cancel()
	res, err := c.Runner.Run(ctx, system.Cmd{
		Path:    c.Binary,
		Args:    c.globals("schema"),
		Label:   "ratline schema",
		Timeout: c.ReadTimeout,
	})
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "asking ratline for its command surface").
			WithHint("check that %s runs: try 'ratline version'", c.Binary)
	}
	return ParseSchema([]byte(res.Stdout))
}

// globals prefixes an invocation with the flags the panel always sets.
func (c *Client) globals(args ...string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args...)
	if c.ConfigPath != "" {
		out = append(out, "--config="+c.ConfigPath)
	}
	return out
}

// Outcome is what one invocation produced.
type Outcome struct {
	Argv     []string
	Envelope *Envelope
	ExitCode int
	// Logs is what ratline wrote to stderr: its progress lines, and the place a
	// failure explains itself. Under --json stdout carries the envelope and
	// nothing else, so this is the human-readable half.
	Logs     string
	Duration time.Duration

	// stdout is kept for RunText. Not exported: everything else must read the
	// envelope rather than the bytes, so that a command's output shape is a
	// contract rather than something each caller re-guesses.
	stdout string
}

// Failed reports whether the invocation did not succeed.
func (o *Outcome) Failed() bool {
	return o == nil || o.ExitCode != 0 || o.Envelope == nil || !o.Envelope.OK
}

// Err returns the typed error the invocation produced, if any.
func (o *Outcome) Err() error {
	if o == nil {
		return rlerr.Genericf("the command produced no result")
	}
	if o.Envelope != nil {
		if err := o.Envelope.Err(); err != nil {
			return err
		}
	}
	if o.ExitCode != 0 {
		// A non-zero exit with no envelope means ratline died before it could write
		// one — a panic, a signal, or a binary that is not ratline at all.
		return rlerr.Externalf("ratline exited %d without reporting why", o.ExitCode).
			WithHint("the last lines of its log: %s", lastLines(o.Logs, 3))
	}
	return nil
}

// Run executes a request and returns what it produced.
//
// A non-zero exit is not an error here. Exit codes are ratline's way of saying what
// went wrong, and the envelope on stdout says it in words — so both are returned and
// the caller decides. Only a failure to run the binary at all is an error.
func (c *Client) Run(ctx context.Context, cat *Catalogue, policy Policy, req Request) (*Outcome, error) {
	argv, err := BuildArgv(cat, policy, req)
	if err != nil {
		return nil, err
	}
	timeout := c.WriteTimeout
	if cmd, ok := cat.Leaves[req.Verb]; ok && !cmd.Mutates {
		timeout = c.ReadTimeout
	}
	if policy.Long {
		timeout = c.JobTimeout
	}
	return c.exec(ctx, argv, req.Secret, timeout, nil)
}

// RunText runs a read-only command whose output is text rather than an envelope.
//
// `site logs` is the case, and it is not an inconsistency in ratline: its job is to
// put a log on your screen, so it streams journalctl or tail through to stdout and
// --json has nothing to wrap. Parsing an envelope out of that would fail on every
// invocation, so the panel asks for the text and says so.
func (c *Client) RunText(ctx context.Context, cat *Catalogue, policy Policy, req Request) (string, error) {
	cmd, ok := cat.Leaves[req.Verb]
	if !ok {
		return "", rlerr.Usagef("%q is not a ratline command", req.Verb)
	}
	if cmd.Mutates {
		return "", rlerr.Genericf("internal error: %q mutates and cannot be read as text", req.Verb)
	}
	argv, err := BuildArgv(cat, policy, req)
	if err != nil {
		return "", err
	}
	out, err := c.exec(ctx, argv, "", c.ReadTimeout, nil)
	if err != nil {
		return "", err
	}
	if out.ExitCode != 0 {
		return "", rlerr.Externalf("ratline %s exited %d", req.Verb, out.ExitCode).
			WithHint("%s", lastLines(out.Logs, 2))
	}
	return out.stdout, nil
}

// Stream is Run with the log written to w as it arrives, for a job somebody is
// watching. stdout is not streamed: it carries exactly one JSON object, and half of
// one is not useful to anybody.
func (c *Client) Stream(ctx context.Context, cat *Catalogue, policy Policy, req Request, w io.Writer) (*Outcome, error) {
	argv, err := BuildArgv(cat, policy, req)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, argv, req.Secret, c.JobTimeout, w)
}

func (c *Client) exec(ctx context.Context, argv []string, secret string, timeout time.Duration, logs io.Writer) (*Outcome, error) {
	cmd := system.Cmd{
		Path:    c.Binary,
		Args:    c.globals(argv...),
		Label:   "ratline " + strings.Join(argv[:min(2, len(argv))], " "),
		Timeout: timeout,
		Stderr:  logs,
	}
	if secret != "" {
		// The one way a secret reaches ratline. It is not in argv, so it is not in
		// /proc/PID/cmdline, so a tenant on the same box cannot read it out of the
		// process table while the command runs.
		cmd.Stdin = strings.NewReader(secret)
	}
	start := time.Now()
	res, runErr := c.Runner.Run(ctx, cmd)
	if res == nil {
		return nil, rlerr.Wrap(runErr, rlerr.CodeExternal, "running ratline")
	}
	out := &Outcome{
		Argv:     res.Args,
		ExitCode: res.ExitCode,
		Logs:     res.Stderr,
		Duration: time.Since(start),
		stdout:   res.Stdout,
	}
	// The exit status came from the process, so runErr for a non-zero exit is the
	// Runner describing something the envelope describes better. It is only fatal
	// when there is no envelope to read.
	env, perr := ParseEnvelope(res.Stdout)
	if perr != nil {
		if runErr != nil {
			// Deliberately nil. The command ran and failed, and the failure is
			// already described by the exit code and the captured log, which
			// Outcome.Err() turns into a typed error for the caller. Returning
			// the parse error here would replace "certbot could not reach the
			// challenge" with "ratline produced no output".
			//nolint:nilerr // the outcome carries the failure; see above
			return out, nil
		}
		return out, perr
	}
	out.Envelope = env
	return out, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " · ")
}
