package cli

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Command groups shown in `ratline --help`.
const (
	GroupUsers    = "users"
	GroupKeys     = "keys"
	GroupSites    = "sites"
	GroupCerts    = "certs"
	GroupRuntimes = "runtimes"
	GroupOps      = "operations"
)

// Command annotations. These drive the behaviour every command shares —
// privilege checks, locking, dry-run — so a new command opts in by declaring
// what it is rather than by repeating boilerplate.
const (
	// AnnoMutates marks a command that changes system state. It takes the
	// global lock and honours --dry-run.
	AnnoMutates = "ratline_mutates"
	// AnnoAllowNonRoot marks a command that works without EUID 0.
	AnnoAllowNonRoot = "ratline_allow_nonroot"
	// AnnoSkipLock marks a mutating command that must not take the lock,
	// for the certbot deploy hook, which runs while an issue command holds it.
	AnnoSkipLock = "ratline_skip_lock"
	// AnnoRequiredFlag marks a flag the command refuses to run without.
	//
	// ratline enforces required flags by hand, in the command, because the messages are
	// worth writing — "a user is scoped to one database; that is what makes it
	// least-privilege" beats cobra's generic refusal. But that left nothing declarative
	// for the interactive layer to read, so the menu offered `--owner` and `--database`
	// in a list of optional extras, let the operator confirm, and only then did the
	// command refuse. This is the marker that stops the two disagreeing.
	AnnoRequiredFlag = "ratline_required"
	// AnnoOwnWizard marks a command that collects its own input interactively.
	//
	// Four commands have hand-written wizards that do more than ask for flags —
	// `site add` sniffs the project to suggest a runtime, `key add` reads a public key
	// from a file or a URL. The generic -i collector must leave those alone, or an
	// operator gets asked for everything twice.
	AnnoOwnWizard = "ratline_own_wizard"
)

// Mutating marks a command as changing state.
func Mutating(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnoMutates) }

// NonRoot marks a command as runnable without root.
func NonRoot(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnoAllowNonRoot) }

// SkipLock marks a mutating command that must not take the global lock.
func SkipLock(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnoSkipLock) }

// OwnWizard marks a command that collects its own input under -i.
func OwnWizard(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnoOwnWizard) }

// Required marks flags the command will refuse to run without, so the interactive layer
// asks for them rather than offering them among the optional extras.
//
// It does not enforce anything: the command still does that, with its own message. This
// only tells the menu and -i what to ask for first.
func Required(cmd *cobra.Command, names ...string) *cobra.Command {
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil {
			// A typo here would silently un-require a flag, which is how this class of
			// bug started. Panicking at construction means it cannot reach a server.
			panic("cli: Required names --" + n + ", which " + cmd.Name() + " does not have")
		}
		if f.Annotations == nil {
			f.Annotations = map[string][]string{}
		}
		f.Annotations[AnnoRequiredFlag] = []string{"true"}
	}
	return cmd
}

// requiredFlag reports whether a flag was marked with Required.
func requiredFlag(f *pflag.Flag) bool {
	return f != nil && len(f.Annotations[AnnoRequiredFlag]) > 0
}

func annotate(cmd *cobra.Command, key string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = "true"
	return cmd
}

func annotated(cmd *cobra.Command, key string) bool {
	return cmd.Annotations != nil && cmd.Annotations[key] == "true"
}

const rootLong = `ratline provisions and manages isolated users, their sites and their
certificates on a single server.

Each user is a tenant sandbox: its own home, group, shell and SSH keys. Each
site belongs to one user, is served by nginx from inside that user's home, and —
for the node and python runtimes — runs under its own systemd unit as that user
behind a Unix socket.

Every command is safe to run twice, and every mutation is staged, verified and
rolled back as a unit.`

const rootExamples = `  # A tenant with key-only SSH access
  ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub

  # A FastAPI application behind Gunicorn and Uvicorn on a Unix socket
  ratline site add api.example.com --user acme --runtime python --app-module app.main:app

  # A certificate once DNS points at this server
  ratline cert issue api.example.com --email admin@example.com`

// NewRootCommand builds the command tree.
func NewRootCommand(g *Globals) *cobra.Command {
	// Groups are declared in the order they should appear rather than sorted.
	cobra.EnableCommandSorting = false

	root := &cobra.Command{
		Use:               "ratline",
		Short:             "Provision isolated users, sites and certificates on one server",
		Long:              rootLong,
		Example:           rootExamples,
		SilenceErrors:     true, // Main prints errors, with hints
		SilenceUsage:      true, // a wall of usage text buries the actual problem
		DisableAutoGenTag: true,
		Args:              cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return g.setup(cmd) },
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return rlerr.Usagef("unknown command %q", args[0]).
					WithHint("run 'ratline --help' for the list of commands")
			}
			// A bare `ratline` on a terminal opens the menu; anywhere else it
			// prints the grouped help.
			if g.CanPrompt() {
				return runMainMenu(cmd.Context(), g)
			}
			return cmd.Help()
		},
	}
	// The root command itself is reachable without root so that an unknown
	// command or a bad flag reports a usage error (exit 2) rather than
	// complaining about privileges first. Subcommands are checked individually,
	// because PersistentPreRunE receives the command actually being run — and
	// the interactive menu requires root itself, at the point it needs it.
	NonRoot(root)
	g.bind(root.PersistentFlags())

	// Turn cobra's flag errors into ratline usage errors so they exit 2 with a
	// hint instead of exit 1 with a bare message.
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return rlerr.Wrap(err, rlerr.CodeUsage, "invalid flags for %q", c.CommandPath()).
			WithHint("run '%s --help' for the accepted flags", c.CommandPath())
	})

	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(helpTemplate)

	addGrouped(root,
		newUserCommand(g),
		newKeyCommand(g),
		newSiteCommand(g),
		newCertCommand(g),
		newRuntimeCommand(g),
		newInitCommand(g),
		newBackupCommand(g),
		newRestoreCommand(g),
		newDBCommand(g),
		newConfigCommand(g),
		newDoctorCommand(g),
		newStatusCommand(g),
		newTroubleshootCommand(g),
		newExplainCommand(g),
		newReconcileCommand(g),
		newExportCommand(g),
		newUpdateCommand(g),
		newVersionCommand(g),
		newManCommand(g),
	)
	// `completion` is cobra's own, and needs neither root nor a config file.
	// Cobra adds it lazily during Execute, so it has to be realised here before
	// the annotation can be attached — otherwise `ratline completion zsh` refuses
	// to run for a non-root operator setting up their own shell.
	root.InitDefaultCompletionCmd()
	// After the tree is complete, so every command is reachable by the traversal.
	registerCompletions(g, root)
	for _, c := range root.Commands() {
		switch c.Name() {
		case "completion", "help":
			NonRoot(c)
			for _, sub := range c.Commands() {
				NonRoot(sub)
			}
		}
	}
	return root
}

// groupOrder is the order the groups appear in `ratline --help`, chosen to
// follow the order an operator does the work in: a user, then its keys, then
// its sites, then their certificates.
var groupOrder = []*cobra.Group{
	{ID: GroupUsers, Title: "USERS"},
	{ID: GroupKeys, Title: "SSH KEYS"},
	{ID: GroupSites, Title: "SITES"},
	{ID: GroupCerts, Title: "CERTIFICATES"},
	{ID: GroupRuntimes, Title: "RUNTIMES"},
	{ID: GroupOps, Title: "OPERATIONS"},
}

// addGrouped registers commands, declaring only the groups that have members.
// An empty heading in the help output would be noise while the command tree is
// still being filled in.
func addGrouped(root *cobra.Command, cmds ...*cobra.Command) {
	used := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		if c.GroupID != "" {
			used[c.GroupID] = true
		}
	}
	for _, grp := range groupOrder {
		if used[grp.ID] {
			root.AddGroup(grp)
		}
	}
	root.AddCommand(cmds...)
}

// Main runs the CLI against the real process streams and returns the exit code.
func Main(args []string) int { return Run(NewGlobals(), args) }

// Run executes the CLI with an explicit Globals, so tests can supply their own
// streams and inspect what a command wrote.
func Run(g *Globals, args []string) int {
	g.Argv = append([]string{"ratline"}, args...)

	root := NewRootCommand(g)

	// `ratline restor --help` — a typo — printed the root help and exited 0, saying
	// nothing about `restor` not existing. cobra's help flag is handled before RunE, so
	// the unknown-command check there never runs, and the operator is left reading a
	// list of commands wondering which one they got wrong. Without --help the same typo
	// correctly exits 2, which made the inconsistency easy to miss.
	if name, ok := unknownCommandWithHelp(root, args); ok {
		err := rlerr.Usagef("unknown command %q", name).
			WithHint("run 'ratline --help' for the list of commands")
		g.reportError(err)
		return rlerr.ExitCode(err)
	}

	root.SetArgs(args)
	root.SetOut(g.Stdout)
	root.SetErr(g.Stderr)

	// A cancelled context reaches long-running steps through cmd.Context(), so
	// Ctrl-C unwinds the rollback stack rather than abandoning a half-built site.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := classifyCobraError(root.ExecuteContext(ctx))
	code := rlerr.ExitCode(err)
	if err != nil {
		g.reportError(err)
	}
	g.teardown(err, code)
	return code
}

// completionSubcommands is set up by cobra itself; its errors, like unknown
// commands, arrive as plain errors. Mapping the recognisable ones onto exit 2
// keeps the exit-code contract honest for the most common operator mistake.
func classifyCobraError(err error) error {
	if err == nil {
		return nil
	}
	if rlerr.CodeOf(err) != rlerr.CodeGeneric {
		return err
	}
	msg := err.Error()
	for _, p := range []string{
		"unknown command", "unknown flag", "unknown shorthand flag",
		"invalid argument", "accepts ", "requires at least", "flag needs an argument",
	} {
		if strings.Contains(msg, p) {
			return rlerr.Wrap(err, rlerr.CodeUsage, "invalid invocation").
				WithHint("run 'ratline --help' for the list of commands")
		}
	}
	return err
}

// usageTemplate is cobra's, with Examples moved below the flags: the examples
// are the most useful part of a help page and they belong where the eye lands
// last, immediately above the prompt.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

OTHER{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

// unknownCommandWithHelp reports an argument that looks like a subcommand, is not one,
// and is accompanied by a help flag.
//
// The subtlety is that a flag can take its value as the next argument, so a naive scan
// for "the first thing not starting with a dash" reads the value of --config as a command
// name. Each flag is therefore looked up: one whose NoOptDefVal is empty consumes the
// argument after it.
//
// Only the first real non-flag argument matters. Anything after it belongs to whatever
// command it named, and a command's own arguments are its own business.
func unknownCommandWithHelp(root *cobra.Command, args []string) (string, bool) {
	flags := root.PersistentFlags()
	var wantsHelp bool
	var first string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			wantsHelp = true
		case a == "--":
			// Everything after this is positional by definition.
			if first == "" && i+1 < len(args) {
				first = args[i+1]
			}
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, _, hasValue := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			if hasValue {
				continue
			}
			// A flag that is not boolean-like takes the next argument as its value.
			if f := flags.Lookup(name); f != nil && f.NoOptDefVal == "" {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			shorthand := a[len(a)-1:]
			if f := flags.ShorthandLookup(shorthand); f != nil && f.NoOptDefVal == "" {
				i++
			}
		case first == "":
			first = a
		}
	}
	if !wantsHelp || first == "" {
		return "", false
	}
	for _, c := range root.Commands() {
		if c.Name() == first || c.HasAlias(first) {
			return "", false
		}
	}
	return first, true
}
