package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Globals holds the global flags and everything derived from them. One instance
// exists per process and is handed to each command, so a command never reaches
// for package state and the future HTTP layer can construct its own.
type Globals struct {
	// Global flags.
	JSON        bool
	Quiet       bool
	Verbose     bool
	DryRun      bool
	Yes         bool
	Interactive bool
	NoInput     bool
	ConfigPath  string

	// Streams. Tests replace these.
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	StdinTTY  bool
	StdoutTTY bool
	StderrTTY bool
	Color     bool

	// completionMode is set when this invocation is cobra's hidden completion
	// helper rather than a command an operator typed. See setup.
	completionMode bool
	Width          int

	Cfg     *config.Config
	Log     *log.Logger
	Bins    *system.Binaries
	Runner  system.Runner
	Audit   log.Auditor
	Invoker system.Invoker
	OS      system.OSInfo

	Start   time.Time
	Argv    []string
	CmdPath string

	lock  *system.Lock
	store *state.Store
}

// Store opens the state database on first use.
//
// It is lazy because several commands — version, completion, man — must work on
// a server where ratline has never run, and opening the database would create
// /var/lib/ratline as a side effect of asking for a version number.
func (g *Globals) Store(ctx context.Context) (*state.Store, error) {
	if g.store != nil {
		return g.store, nil
	}
	if g.Cfg == nil {
		return nil, rlerr.Genericf("internal error: the configuration was not loaded")
	}
	s, err := state.Open(g.Cfg.Paths.StateDB)
	if err != nil {
		return nil, err
	}
	g.store = s
	return s, nil
}

// Invoked names the operator for state and audit records.
func (g *Globals) Invoked() string { return g.Invoker.Display() }

// NewGlobals returns Globals bound to the real process streams.
func NewGlobals() *Globals {
	return &Globals{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Start:  time.Now(),
		Audit:  log.NopAudit(),
		Log:    log.New(log.Options{Out: os.Stderr, Level: log.LevelInfo}),
	}
}

func (g *Globals) bind(fs *pflag.FlagSet) {
	fs.BoolVar(&g.JSON, "json", false, "Machine-readable output on stdout; logs on stderr")
	fs.BoolVarP(&g.Quiet, "quiet", "q", false, "Errors only")
	fs.BoolVarP(&g.Verbose, "verbose", "v", false, "Debug logging")
	fs.BoolVar(&g.DryRun, "dry-run", false, "Print every mutation without making it")
	fs.BoolVarP(&g.Yes, "yes", "y", false, "Assume yes; required for destructive operations without a terminal")
	fs.BoolVarP(&g.Interactive, "interactive", "i", false, "Prompt for whatever was not supplied as a flag")
	fs.BoolVar(&g.NoInput, "no-input", false, "Never prompt; fail instead (implied when stdout is not a terminal)")
	fs.StringVar(&g.ConfigPath, "config", "", "Configuration file (default "+config.DefaultPath+")")
}

// setup runs before every command. It resolves the flags against each other and
// the terminal, builds the logger, loads the configuration, checks privileges
// and takes the lock for mutating commands.
func (g *Globals) setup(cmd *cobra.Command) error {
	g.CmdPath = cmd.CommandPath()

	if err := g.resolve(); err != nil {
		return err
	}

	// Shell completion is a different kind of invocation and has to be treated as
	// one. It runs as whoever pressed Tab, which is usually not root, and cobra
	// reserves stdout for the candidate list — so a privilege refusal here would
	// both break completion for every non-root operator and be offered to them as a
	// completion candidate. The lookups degrade to no candidates on their own when
	// they cannot read state, which is the right answer.
	if isCompletionRequest(cmd) {
		g.completionMode = true
	}

	level := g.level()
	if g.completionMode {
		// Even on stderr, log output during completion is noise printed over the
		// shell's prompt.
		level = log.LevelError
	}
	g.Log = log.New(log.Options{
		Out:   g.Stderr,
		Level: level,
		JSON:  g.JSON,
		Color: g.Color,
	})

	// Anything created without an explicit mode still lands at 0640/0750.
	system.SetUmask(system.ProvisioningUmask)

	g.Invoker = system.CurrentInvoker()
	g.OS = system.DetectOS()

	if !annotated(cmd, AnnoAllowNonRoot) && !g.completionMode {
		if err := system.RequireRoot(); err != nil {
			return err
		}
		if err := system.CheckSelfBinary(); err != nil {
			return err
		}
	}

	cfg, err := config.LoadOrDefault(g.configPath())
	if err != nil {
		return err
	}
	g.Cfg = cfg
	if !cfg.Loaded && annotated(cmd, AnnoMutates) {
		g.Log.Warn("no configuration file; using built-in defaults",
			"path", cfg.SourcePath, "fix", "run 'ratline init'")
	}
	// A --verbose or --quiet flag beats the file; otherwise the file decides.
	if !g.Verbose && !g.Quiet && !g.completionMode && cfg.Logging.Level != "" {
		g.Log = log.New(log.Options{Out: g.Stderr, Level: cfg.LogLevel(), JSON: g.JSON, Color: g.Color})
	}

	g.Bins = system.NewBinaries()
	if err := g.Bins.LoadOverridesFromEnv(os.Environ()); err != nil {
		return err
	}
	g.Runner = system.NewRunner(g.Bins, g.Log, g.DryRun)

	if a, err := log.OpenAudit(cfg.Paths.AuditLog); err != nil {
		// Losing the audit trail must not stop an operator managing the server.
		g.Log.Debug("audit log unavailable", "err", err)
	} else {
		g.Audit = a
	}

	// Never take the lock for a completion lookup: pressing Tab would block on
	// whatever mutation is in flight, and completing the arguments of a mutating
	// command is not itself a mutation.
	if annotated(cmd, AnnoMutates) && !g.DryRun && !annotated(cmd, AnnoSkipLock) && !g.completionMode {
		l, err := system.AcquireLock(cfg.Paths.Lock, cfg.Defaults.LockTimeout.D(), g.CmdPath)
		if err != nil {
			return err
		}
		g.lock = l
	}
	if g.DryRun {
		g.Log.Info("dry run: no changes will be made")
	}
	return nil
}

// resolve settles the interactions between the global flags and the terminal.
func (g *Globals) resolve() error {
	if g.Quiet && g.Verbose {
		return rlerr.Usagef("--quiet and --verbose contradict each other")
	}
	if g.JSON && g.Interactive {
		return rlerr.Usagef("--json and --interactive contradict each other").
			WithHint("--json exists for automation, which cannot answer prompts")
	}
	// Rather than silently picking a winner, refuse. An operator who typed both
	// has one of them wrong, and guessing which would be worse than saying so.
	if g.Interactive && g.NoInput {
		return rlerr.Usagef("--interactive and --no-input contradict each other")
	}
	if g.Interactive && g.Yes {
		return rlerr.Usagef("--interactive and --yes contradict each other").
			WithHint("--yes suppresses every prompt, including the wizard's")
	}

	g.StdinTTY = isTTY(g.Stdin)
	g.StdoutTTY = isTTY(g.Stdout)
	g.StderrTTY = isTTY(g.Stderr)
	g.Width = terminalWidth(g.Stderr)

	// Documented rule: --no-input is implied when stdout is not a terminal.
	// This is what keeps a prompt from hanging a CI pipeline forever.
	if !g.StdoutTTY || g.Yes {
		g.NoInput = true
	}

	colorMode := "auto"
	if g.Cfg != nil {
		colorMode = g.Cfg.Logging.Color
	}
	switch {
	case g.JSON, os.Getenv("NO_COLOR") != "", os.Getenv("TERM") == "dumb", colorMode == "never":
		g.Color = false
	case colorMode == "always":
		g.Color = true
	default:
		g.Color = g.StderrTTY
	}
	return nil
}

func (g *Globals) level() log.Level {
	switch {
	case g.Verbose:
		return log.LevelDebug
	case g.Quiet:
		return log.LevelError
	default:
		return log.LevelInfo
	}
}

func (g *Globals) configPath() string {
	if g.ConfigPath != "" {
		return g.ConfigPath
	}
	if p := config.EnvConfigPath(); p != "" {
		return p
	}
	return config.DefaultPath
}

// CanPrompt reports whether an interactive question can actually be answered.
// A prompt needs a terminal on both ends: stdin to read and stderr to draw on.
func (g *Globals) CanPrompt() bool {
	return !g.NoInput && !g.JSON && g.StdinTTY && g.StderrTTY
}

// Plain reports whether the interface should degrade to line-based prompts:
// no colour, a dumb terminal, or a window too narrow for a form.
func (g *Globals) Plain() bool {
	return !g.Color || os.Getenv("TERM") == "dumb" || (g.Width > 0 && g.Width < 60)
}

// Printf writes to stdout. Suppressed under --json so that stdout stays a
// single parseable object, and under --quiet.
func (g *Globals) Printf(format string, a ...any) {
	if g.JSON || g.Quiet {
		return
	}
	fmt.Fprintf(g.Stdout, format, a...)
}

// Println writes a line to stdout under the same rules as Printf.
func (g *Globals) Println(a ...any) {
	if g.JSON || g.Quiet {
		return
	}
	fmt.Fprintln(g.Stdout, a...)
}

// Confirm asks a yes/no question. --yes answers yes; without a terminal it is
// an error rather than an assumption.
func (g *Globals) Confirm(question string) (bool, error) {
	if g.Yes {
		g.Log.Debug("assuming yes", "question", question)
		return true, nil
	}
	if !g.CanPrompt() {
		return false, rlerr.InputRequiredf("%s", question).
			WithHint("pass --yes to answer yes without being asked")
	}
	fmt.Fprintf(g.Stderr, "%s [y/N]: ", question)
	line, err := readLine(g.Stdin)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// ConfirmTyped requires the operator to type an exact token — a username or a
// domain — before a destructive operation proceeds. A bare y/N is too easy to
// hit by reflex when the thing being deleted is someone's site.
func (g *Globals) ConfirmTyped(expect, question string) error {
	if g.Yes {
		g.Log.Warn("destructive operation confirmed by --yes", "target", expect)
		return nil
	}
	if !g.CanPrompt() {
		return rlerr.InputRequiredf("%s", question).
			WithHint("this operation cannot be confirmed without a terminal; pass --yes to proceed")
	}
	fmt.Fprintf(g.Stderr, "%s\nType %q to confirm: ", question, expect)
	line, err := readLine(g.Stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != expect {
		return rlerr.Usagef("confirmation did not match %q; nothing was changed", expect)
	}
	return nil
}

// teardown releases the lock and writes the audit record. It runs whether the
// command succeeded or failed, because a failed mutation is exactly what an
// operator will later want to find in the trail.
func (g *Globals) teardown(err error, code int) {
	if g.store != nil {
		if cerr := g.store.Close(); cerr != nil && g.Log != nil {
			g.Log.Debug("could not close the state database", "err", cerr)
		}
		g.store = nil
	}
	if g.lock != nil {
		if rerr := g.lock.Release(); rerr != nil && g.Log != nil {
			g.Log.Warn("could not release the lock", "err", rerr)
		}
		g.lock = nil
	}
	if g.Audit == nil {
		return
	}
	result := "ok"
	msg := ""
	if err != nil {
		result = "error"
		msg = err.Error()
	}
	entry := log.Entry{
		Version:    buildinfo.Short(),
		Command:    g.CmdPath,
		Argv:       g.Argv,
		UID:        g.Invoker.UID,
		User:       g.Invoker.Name,
		SudoUser:   g.Invoker.SudoUser,
		DryRun:     g.DryRun,
		Result:     result,
		ExitCode:   code,
		DurationMS: time.Since(g.Start).Milliseconds(),
		Error:      msg,
		Fields:     rlerr.Fields(err),
	}
	if werr := g.Audit.Write(entry); werr != nil && g.Log != nil {
		g.Log.Debug("could not write the audit entry", "err", werr)
	}
	_ = g.Audit.Close()
}

func isTTY(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func terminalWidth(v any) int {
	f, ok := v.(*os.File)
	if !ok {
		return 0
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// readSecret reads a value without echoing it. Terminal echo is disabled where
// possible; where it is not, the operator is told so rather than having their
// password appear on screen without warning.
func (g *Globals) readSecret(prompt string) (string, error) {
	if !g.CanPrompt() {
		return "", rlerr.InputRequiredf("a secret is required but there is no terminal to read it from").
			WithHint("pipe it in with --stdin instead")
	}
	fmt.Fprint(g.Stderr, prompt)
	f, ok := g.Stdin.(*os.File)
	if !ok {
		line, err := readLine(g.Stdin)
		return strings.TrimSpace(line), err
	}
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(g.Stderr)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeInputRequired, "reading the secret")
	}
	return string(b), nil
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", rlerr.InputRequiredf("could not read a reply from the terminal")
	}
	return line, nil
}
