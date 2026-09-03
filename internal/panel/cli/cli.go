package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// App is the process-wide state a command needs, the same shape as ratline's Globals
// and for the same reason: a command never reaches for package state, and a test can
// construct its own.
type App struct {
	ConfigPath string
	Cfg        *panel.Config
	Log        *log.Logger
	Runner     system.Runner
	Bins       *system.Binaries

	JSON    bool
	Verbose bool
	Quiet   bool
	DryRun  bool
	Yes     bool

	Stdout *os.File
	Stderr *os.File
	Stdin  *os.File
}

// NewApp returns an App bound to the real process streams.
func NewApp() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin}
}

const rootLong = `ratline-panel is a web interface for ratline.

It runs as a service on the same server, listens on a port, and can be put behind a
domain with a certificate. Signing in gives an administrator the same operations
ratline offers at the command line — tenants, sites, deploys, certificates, keys and
databases — in a browser.

It does not reimplement any of them. Every action runs the ratline binary with --json
and reads the envelope, so a mutation made from the panel is staged, verified,
committed and rolled back by exactly the code that would have run had somebody typed
it over SSH.

Two kinds of account: a super admin, who can invite others and run the operations
that cannot be undone, and an admin, who runs the server day to day.`

const rootExamples = `  # Install the service and open it on the loopback
  ratline-panel install --email you@example.com

  # Put it on a domain with a certificate, once DNS points here
  ratline-panel domain set panel.example.com --email you@example.com

  # Recover access without a browser
  ratline-panel account list
  ratline-panel account role you@example.com superadmin`

// NewRootCommand builds the command tree.
func NewRootCommand(app *App) *cobra.Command {
	cobra.EnableCommandSorting = false

	root := &cobra.Command{
		Use:               "ratline-panel",
		Short:             "A web interface for ratline",
		Long:              rootLong,
		Example:           rootExamples,
		SilenceErrors:     true,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return app.setup(cmd) },
		RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	f := root.PersistentFlags()
	f.StringVar(&app.ConfigPath, "config", "", "Configuration file (default "+panel.DefaultConfigPath+")")
	f.BoolVar(&app.JSON, "json", false, "Machine-readable output on stdout; logs on stderr")
	f.BoolVarP(&app.Verbose, "verbose", "v", false, "Debug logging")
	f.BoolVarP(&app.Quiet, "quiet", "q", false, "Errors only")
	f.BoolVar(&app.DryRun, "dry-run", false, "Print every mutation without making it")
	f.BoolVarP(&app.Yes, "yes", "y", false, "Assume yes; required for destructive operations without a terminal")

	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return rlerr.Wrap(err, rlerr.CodeUsage, "invalid flags for %q", c.CommandPath()).
			WithHint("run '%s --help' for the accepted flags", c.CommandPath())
	})

	root.AddCommand(
		newServeCommand(app),
		newInstallCommand(app),
		newUninstallCommand(app),
		newDomainCommand(app),
		newAccountCommand(app),
		newNginxCommand(app),
		newDoctorCommand(app),
		newConfigCommand(app),
		newVersionCommand(app),
	)
	return root
}

// commandsWithoutRoot are the ones that answer without touching the system.
var commandsWithoutRoot = map[string]bool{
	"ratline-panel version":          true,
	"ratline-panel help":             true,
	"ratline-panel config reference": true,
}

func (app *App) setup(cmd *cobra.Command) error {
	if app.Quiet && app.Verbose {
		return rlerr.Usagef("--quiet and --verbose contradict each other")
	}
	level := log.LevelInfo
	switch {
	case app.Verbose:
		level = log.LevelDebug
	case app.Quiet:
		level = log.LevelError
	}
	app.Log = log.New(log.Options{
		Out: app.Stderr, Level: level, JSON: app.JSON,
		Color: !app.JSON && os.Getenv("NO_COLOR") == "",
	})

	if !commandsWithoutRoot[cmd.CommandPath()] {
		// Everything else reads a database holding password hashes, or runs a
		// binary that provisions system accounts.
		if err := system.RequireRoot(); err != nil {
			return err
		}
	}
	system.SetUmask(system.ProvisioningUmask)

	cfg, err := panel.LoadOrDefault(panel.ConfigPath(app.ConfigPath))
	if err != nil {
		return err
	}
	app.Cfg = cfg

	app.Bins = system.NewBinaries()
	if err := app.Bins.LoadOverridesFromEnv(os.Environ()); err != nil {
		return err
	}
	app.Runner = system.NewRunner(app.Bins, app.Log, app.DryRun)
	return nil
}

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	app := NewApp()
	root := NewRootCommand(app)
	root.SetArgs(args)
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	// The same exit-code contract ratline publishes, on purpose: a script driving
	// both branches on one set of numbers.
	if app.JSON {
		code := rlerr.CodeOf(err)
		fmt.Fprintf(app.Stdout,
			"{\n  \"ok\": false,\n  \"error\": {\n    \"code\": %d,\n    \"name\": %q,\n    \"message\": %q\n  }\n}\n",
			int(code), code.Name(), err.Error())
	} else {
		fmt.Fprintf(app.Stderr, "error: %s\n", err.Error())
		if hint := rlerr.Hint(err); hint != "" {
			fmt.Fprintf(app.Stderr, "  hint: %s\n", hint)
		}
	}
	return rlerr.ExitCode(err)
}

func newVersionCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(app.Stdout, "ratline-panel %s (%s, %s)\n",
				buildinfo.Version, buildinfo.Commit, buildinfo.Date)
			return nil
		},
	}
}
