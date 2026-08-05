package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// maxCapturedOutput bounds what we keep from a child process. A runaway build
// must not exhaust memory just because we wanted its last twenty lines.
const maxCapturedOutput = 1 << 20

// DefaultTimeout applies to any command that does not set one. Long steps
// (npm ci, certbot, source builds) set their own.
const DefaultTimeout = 2 * time.Minute

// Identity is a system account a child process may be dropped to.
type Identity struct {
	Name   string
	UID    int
	GID    int
	Groups []int
	Home   string
	Shell  string
}

// Cmd describes one external command.
//
// There is no field for a command *line*: every invocation is an argv slice.
// ratline never constructs a shell string, so quoting bugs cannot become
// command injection.
type Cmd struct {
	// Exactly one of Name (resolved through the Binaries registry) or Path
	// (an absolute path, for managed runtimes and per-site venv binaries).
	Name string
	Path string

	Args  []string
	Dir   string
	Env   []string // complete replacement; nil means MinimalEnv or UserEnv
	Stdin io.Reader

	// As drops privileges to a site owner for the duration of the command.
	As *Identity

	Timeout time.Duration

	// Stream surfaces child output as it arrives, for steps slow enough that
	// silence looks like a hang.
	Stream bool

	// Mutates marks a command that changes system state. Under --dry-run these
	// are described and skipped; reads still execute, because a preview built
	// from stale facts is worse than no preview.
	Mutates bool

	// OKExit lists additional exit codes that are not failures (grep's 1,
	// systemctl is-active's 3).
	OKExit []int

	// Label names the step in logs; defaults to the binary's base name.
	Label string
}

// Result is the outcome of one command.
type Result struct {
	Path     string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	Skipped  bool // --dry-run skipped a mutating command
}

// Out returns stdout with its trailing newline removed.
func (r *Result) Out() string { return strings.TrimRight(r.Stdout, "\r\n") }

// Lines splits stdout into non-empty lines.
func (r *Result) Lines() []string {
	var out []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// CommandLine renders the invocation for display. It is for humans and audit
// records only — nothing ever parses or executes this string.
func (r *Result) CommandLine() string {
	return strings.Join(append([]string{r.Path}, r.Args...), " ")
}

// Runner executes commands. The interface exists so that tests, --dry-run and
// the future HTTP layer share one seam.
type Runner interface {
	Run(ctx context.Context, c Cmd) (*Result, error)
}

type execRunner struct {
	bins   *Binaries
	log    *log.Logger
	dryRun bool
}

// NewRunner returns the production Runner.
func NewRunner(bins *Binaries, lg *log.Logger, dryRun bool) Runner {
	if lg == nil {
		lg = log.Discard()
	}
	return &execRunner{bins: bins, log: lg, dryRun: dryRun}
}

func (r *execRunner) Run(ctx context.Context, c Cmd) (*Result, error) {
	path, err := r.resolve(c)
	if err != nil {
		return nil, err
	}
	if err := ValidateArgv(c.Args); err != nil {
		return nil, err
	}

	label := c.Label
	if label == "" {
		label = filepath.Base(path)
	}
	display := strings.Join(append([]string{path}, c.Args...), " ")

	if r.dryRun && c.Mutates {
		r.log.Info("would run", "cmd", log.ArgvString(append([]string{path}, c.Args...)))
		return &Result{Path: path, Args: c.Args, Skipped: true}, nil
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	cmd.Env = c.Env
	if cmd.Env == nil {
		if c.As != nil {
			cmd.Env = UserEnv(c.As)
		} else {
			cmd.Env = MinimalEnv()
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if c.As != nil {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:    uint32(c.As.UID),
			Gid:    uint32(c.As.GID),
			Groups: toUint32(c.As.Groups),
		}
	}
	// Kill the whole process group on timeout: build tools spawn children, and
	// killing only the parent leaves them holding the site directory.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr capBuffer
	stdout.max, stderr.max = maxCapturedOutput, maxCapturedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if c.Stream {
		so := r.log.Stream(log.LevelInfo, label)
		se := r.log.Stream(log.LevelInfo, label)
		defer so.Close()
		defer se.Close()
		cmd.Stdout = io.MultiWriter(&stdout, so)
		cmd.Stderr = io.MultiWriter(&stderr, se)
	}

	r.log.Debug("run", "cmd", log.ArgvString(append([]string{path}, c.Args...)), "dir", c.Dir, "as", identityName(c.As))
	start := time.Now()
	runErr := cmd.Run()
	res := &Result{
		Path:     path,
		Args:     c.Args,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, rlerr.Wrap(runErr, rlerr.CodeExternal, "could not run %s", display).
			WithHint("check that %s exists and is executable", path)
	}

	r.log.Debug("ran", "cmd", label, "exit", res.ExitCode, "ms", res.Duration.Milliseconds())

	if res.ExitCode == 0 || containsInt(c.OKExit, res.ExitCode) {
		return res, nil
	}

	// Timeouts and cancellation deserve their own message: "exit status 1" from
	// a killed child tells an operator nothing.
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, rlerr.Externalf("%s timed out after %s", label, timeout).
				WithHint("re-run with --verbose to see its output, or raise the timeout in /etc/ratline/config.yaml")
		}
		return res, rlerr.Externalf("%s was interrupted", label)
	}

	return res, rlerr.Externalf("%s failed (exit %d): %s", label, res.ExitCode, firstMeaningfulLine(res.Stderr, res.Stdout)).
		WithField("command", display).
		WithField("exit_code", fmt.Sprint(res.ExitCode)).
		WithHint("full output: re-run with --verbose")
}

func (r *execRunner) resolve(c Cmd) (string, error) {
	switch {
	case c.Name != "" && c.Path != "":
		return "", rlerr.Genericf("internal error: command %q specifies both Name and Path", c.Name)
	case c.Name != "":
		return r.bins.Path(c.Name)
	case c.Path == "":
		return "", rlerr.Genericf("internal error: command specifies neither Name nor Path")
	case !filepath.IsAbs(c.Path):
		return "", rlerr.Genericf("internal error: command path %q is not absolute", c.Path)
	case filepath.Clean(c.Path) != c.Path:
		return "", rlerr.Genericf("internal error: command path %q is not clean", c.Path)
	default:
		return c.Path, nil
	}
}

// Output runs a read-only command and returns its trimmed stdout.
func Output(ctx context.Context, r Runner, name string, args ...string) (string, error) {
	res, err := r.Run(ctx, Cmd{Name: name, Args: args})
	if err != nil {
		return "", err
	}
	return res.Out(), nil
}

// capBuffer is a bytes.Buffer that stops growing past max.
type capBuffer struct {
	b         bytes.Buffer
	max       int
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.max <= 0 {
		c.max = maxCapturedOutput
	}
	room := c.max - c.b.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.truncated = true
		c.b.Write(p[:room])
		return len(p), nil
	}
	return c.b.Write(p)
}

func (c *capBuffer) String() string {
	if c.truncated {
		return c.b.String() + "\n[output truncated]"
	}
	return c.b.String()
}

// firstMeaningfulLine picks the line most likely to explain a failure: the last
// non-empty line of stderr, falling back to stdout.
func firstMeaningfulLine(candidates ...string) string {
	// The reason is at the end, so this reads backwards — but the last line of a
	// failure is usually the tool telling you where to look rather than what went
	// wrong. certbot signs off with "Ask for help ... See the logfile ...", and that
	// is what an operator saw as the reason a renewal failed. Skip the signposts and
	// keep reading up; fall back to them only if there was nothing else.
	var fallback string
	for _, s := range candidates {
		lines := strings.Split(s, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			l := strings.TrimSpace(lines[i])
			if l == "" {
				continue
			}
			if isSignpost(l) {
				if fallback == "" {
					fallback = l
				}
				continue
			}
			return l
		}
	}
	if fallback != "" {
		return fallback
	}
	return "no output"
}

// signposts are lines that point at the reason instead of being it.
var signposts = []string{
	"ask for help",
	"see the logfile",
	"please see the logfile",
	"for more information",
	"for details, see",
	"saving debug log",
	"run with -v",
	"re-run with",
}

func isSignpost(line string) bool {
	// A rule of separators and dashes, which several tools use to frame their output.
	if strings.Trim(line, "-=*_ ") == "" {
		return true
	}
	lower := strings.ToLower(line)
	for _, s := range signposts {
		if strings.HasPrefix(lower, s) {
			return true
		}
	}
	return false
}

// ValidateArgv rejects arguments that cannot safely reach execve or a unit file.
func ValidateArgv(args []string) error {
	for i, a := range args {
		if strings.ContainsRune(a, 0) {
			return rlerr.Usagef("argument %d contains a NUL byte", i+1)
		}
		if strings.ContainsAny(a, "\n\r") {
			return rlerr.Usagef("argument %d contains a newline: %q", i+1, a)
		}
		if len(a) > 4096 {
			return rlerr.Usagef("argument %d is %d bytes long, which exceeds the 4096-byte limit", i+1, len(a))
		}
	}
	return nil
}

func toUint32(in []int) []uint32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]uint32, len(in))
	for i, v := range in {
		out[i] = uint32(v)
	}
	return out
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func identityName(id *Identity) string {
	if id == nil {
		return "root"
	}
	return id.Name
}
