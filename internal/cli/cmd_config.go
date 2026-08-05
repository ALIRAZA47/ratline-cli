package cli

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline config` reads and writes /etc/ratline/config.yaml.
//
// Every setting was previously changed by editing the file by hand, which is fine until
// the thing being set is a mode, a boolean somebody spells "yes", or a path that has to
// exist. So this validates the whole file after every change and puts the old one back if
// it no longer loads — the same staged-verify-commit shape as everything else here.
//
// It edits the file textually rather than re-encoding it. The shipped configuration is
// documentation: every setting carries a comment explaining what it does and why the
// default is what it is, and re-encoding the struct destroys all of it.

func newConfigCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Read and change ratline's own configuration",
		GroupID: GroupOps,
		Long: "The configuration lives at /etc/ratline/config.yaml and is read on every\n" +
			"invocation, so there is nothing to reload after a change.\n\n" +
			"Editing it by hand is still fine — these commands exist because some settings are\n" +
			"easy to get subtly wrong, and because a file that no longer loads breaks every\n" +
			"other command. Every change here is validated before it is committed, and the\n" +
			"previous file is put back if the result would not load.\n\n" +
			"Comments are preserved. The shipped file is the reference — every setting carries\n" +
			"an explanation — so a change that flattened it would cost more than it saved.",
		Example: "  ratline config get acme.email\n" +
			"  ratline config set acme.email ops@example.com\n" +
			"  ratline config set features.db_provisioning true\n" +
			"  ratline config unset defaults.memory_max     # back to the default\n" +
			"  ratline config show --changed\n" +
			"  ratline config reference | less",
	}
	cmd.AddCommand(
		newConfigShowCommand(g),
		newConfigGetCommand(g),
		newConfigSetCommand(g),
		newConfigUnsetCommand(g),
		newConfigPathCommand(g),
		newConfigReferenceCommand(g),
		newConfigEditCommand(g),
		newConfigValidateCommand(g),
	)
	return cmd
}

func newConfigPathCommand(g *Globals) *cobra.Command {
	return NonRoot(&cobra.Command{
		Use:   "path",
		Short: "Print the path of the configuration file in use",
		Args:  cobra.NoArgs,
		Long: "Useful in a script, and worth checking when a change appears not to have taken:\n" +
			"--config and the built-in default can disagree about which file is being read.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"path": g.configPath(), "exists": g.Cfg.Loaded,
				})
			}
			g.Println(g.configPath())
			if !g.Cfg.Loaded {
				g.Log.Warn("that file does not exist, so the built-in defaults are in use",
					"fix", "ratline init")
			}
			return nil
		},
	})
}

func newConfigReferenceCommand(g *Globals) *cobra.Command {
	return NonRoot(&cobra.Command{
		Use:   "reference",
		Short: "Print the shipped configuration, with every setting explained",
		Args:  cobra.NoArgs,
		Long: "The commented file ratline ships, with every default and the reasoning behind it.\n\n" +
			"This is what 'ratline init' writes on a fresh server. Print it to recover a block\n" +
			"you deleted, or to read the explanation of a setting without a browser:\n\n" +
			"    ratline config reference | grep -A 4 renew_before_days",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Written to stdout raw rather than through the JSON envelope: this is a file,
			// and the point is to be able to diff it or pipe it into an editor.
			_, err := g.Stdout.Write(config.DefaultYAML())
			return err
		},
	})
}

func newConfigGetCommand(g *Globals) *cobra.Command {
	return NonRoot(&cobra.Command{
		Use:   "get <setting>",
		Short: "Print one setting's value",
		Args:  cobra.ExactArgs(1),
		Long: "Prints the value in effect, and says whether it comes from the file or from the\n" +
			"built-in default. That difference is the useful part: a setting absent from the\n" +
			"file behaves as the default today and would change if the default ever did.",
		Example:           "  ratline config get acme.renew_before_days",
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !config.KeyExists(key) {
				return unknownSetting(key)
			}
			fileValue, fromFile, err := g.configFileValue(key)
			if err != nil {
				return err
			}
			defValue, _, _ := config.GetValue(config.DefaultYAML(), key)

			value := defValue
			source := "default"
			if fromFile {
				value, source = fileValue, g.configPath()
			}
			if config.IsSecret(key) && value != "" && value != `""` {
				value = config.Redacted
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"setting": key, "value": unquote(value),
					"source": source, "default": unquote(defValue),
				})
			}
			g.Println(unquote(value))
			if !fromFile {
				g.Log.Debug("not set in the file; this is the built-in default", "setting", key)
			}
			return nil
		},
	})
}

func newConfigShowCommand(g *Globals) *cobra.Command {
	var changed bool
	cmd := &cobra.Command{
		Use:   "show [prefix]",
		Short: "Show the settings in effect",
		Args:  cobra.MaximumNArgs(1),
		Long: "Every setting, with where its value came from. A prefix narrows it to one\n" +
			"section — 'ratline config show acme' — and --changed shows only what differs from\n" +
			"the shipped defaults, which is usually the question being asked.",
		Example: "  ratline config show --changed\n" +
			"  ratline config show databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) == 1 {
				prefix = args[0]
			}
			type row struct {
				Setting string `json:"setting"`
				Value   string `json:"value"`
				Default string `json:"default"`
				Source  string `json:"source"`
			}
			var rows []row
			for _, key := range config.KnownKeys() {
				if prefix != "" && !strings.HasPrefix(key, prefix) {
					continue
				}
				defValue, _, _ := config.GetValue(config.DefaultYAML(), key)
				fileValue, fromFile, err := g.configFileValue(key)
				if err != nil {
					return err
				}
				value, source := defValue, "default"
				if fromFile {
					value, source = fileValue, "file"
				}
				if changed && unquote(value) == unquote(defValue) {
					continue
				}
				if config.IsSecret(key) {
					if unquote(value) != "" {
						value = config.Redacted
					}
					defValue = ""
				}
				rows = append(rows, row{key, unquote(value), unquote(defValue), source})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Setting < rows[j].Setting })

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"path": g.configPath(), "loaded": g.Cfg.Loaded, "settings": rows,
				})
			}
			if len(rows) == 0 {
				if changed {
					g.Println("Nothing differs from the shipped defaults.")
				} else {
					g.Printf("No settings match %q.\n", prefix)
				}
				return nil
			}
			tbl := g.Table("setting", "value", "source")
			for _, r := range rows {
				tbl.Row(r.Setting, orDash(r.Value), r.Source)
			}
			return tbl.Render()
		},
	}
	cmd.Flags().BoolVar(&changed, "changed", false, "Only settings that differ from the shipped defaults")
	return NonRoot(cmd)
}

func newConfigSetCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <setting> <value>",
		Short: "Change one setting",
		Args:  cobra.ExactArgs(2),
		Long: "Edits the file in place, preserving its comments, then validates the result. If\n" +
			"the change would produce a file that does not load, the previous one is put back\n" +
			"and the error names the setting.\n\n" +
			"An unknown setting is refused rather than written. A typo like paths.systemdir\n" +
			"would otherwise sit in the file being silently ignored, and the misconfiguration\n" +
			"would surface as ratline writing units somewhere nobody looks.",
		Example: "  ratline config set acme.email ops@example.com\n" +
			"  ratline config set features.db_provisioning true\n" +
			"  ratline config set defaults.memory_max 1G",
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if !config.KeyExists(key) {
				return unknownSetting(key)
			}
			// A boolean setting is normalised, so "yes" and "on" work and land in the file
			// as the true or false a YAML parser will read back.
			if isBoolSetting(key) {
				b, err := config.ParseBool(value)
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodeUsage, "%s is a yes-or-no setting", key)
				}
				value = fmt.Sprint(b)
			}
			return g.configEdit(cmd, key, func(body []byte) ([]byte, error) {
				return config.SetValue(body, key, config.FormatScalar(value))
			}, "set "+key+" to "+value)
		},
	}
	return Mutating(cmd)
}

func newConfigUnsetCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <setting>",
		Short: "Remove a setting, so its built-in default applies again",
		Args:  cobra.ExactArgs(1),
		Long: "Deletes the line rather than blanking it. Those are different: an absent setting\n" +
			"takes the built-in default, and an empty one is an explicit empty value — which\n" +
			"for a path or an address is usually not what anybody wants.",
		Example:           "  ratline config unset defaults.memory_max",
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !config.KeyExists(key) {
				return unknownSetting(key)
			}
			return g.configEdit(cmd, key, func(body []byte) ([]byte, error) {
				out, removed, err := config.UnsetValue(body, key)
				if err != nil {
					return nil, err
				}
				if !removed {
					// Not an error: the default is already what applies, which is what
					// was asked for.
					g.Printf("%s is not set in the file; the default already applies.\n", key)
				}
				return out, nil
			}, "unset "+key)
		},
	}
	return Mutating(cmd)
}

func newConfigValidateCommand(g *Globals) *cobra.Command {
	return NonRoot(&cobra.Command{
		Use:   "validate [path]",
		Short: "Check a configuration file without applying it",
		Args:  cobra.MaximumNArgs(1),
		Long: "Loads the file and reports every problem at once rather than the first. Useful\n" +
			"before copying a configuration onto a server, and in CI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := g.configPath()
			if len(args) == 1 {
				path = args[0]
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"path": path, "valid": true})
			}
			g.Printf("%s is valid.\n", path)
			return nil
		},
	})
}

func newConfigEditCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open the configuration in $EDITOR, and refuse to save it broken",
		Args:  cobra.NoArgs,
		Long: "Opens a copy in $EDITOR. On exit the copy is validated, and only replaces the\n" +
			"real file if it loads — so a typo cannot leave every other command broken.\n\n" +
			"The point of going through ratline rather than opening the file directly is that\n" +
			"last part. A configuration file that no longer parses takes the whole tool with it,\n" +
			"and the failure arrives on the next unrelated command.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
			if editor == "" {
				return rlerr.Preconditionf("neither $VISUAL nor $EDITOR is set").
					WithHint("EDITOR=vi ratline config edit, or edit %s directly and then "+
						"run 'ratline config validate'", g.configPath())
			}
			path := g.configPath()
			original, err := os.ReadFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
				}
				original = config.DefaultYAML()
			}

			// Edited in ratline's own run directory rather than beside the real file, so a
			// crashed editor cannot leave a half-written config.yaml.tmp that a later
			// reconcile mistakes for something managed.
			dir := g.Cfg.Paths.RunDir
			if dir == "" {
				dir = os.TempDir()
			}
			if _, err := system.EnsureDir(dir, 0o750, system.KeepUnchanged, system.KeepUnchanged); err != nil {
				return err
			}
			tmp, err := os.CreateTemp(dir, "config-*.yaml")
			if err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "staging the configuration")
			}
			scratch := tmp.Name()
			defer os.Remove(scratch)
			if _, err := tmp.Write(original); err != nil {
				tmp.Close()
				return rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", scratch)
			}
			if err := tmp.Close(); err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", scratch)
			}

			// The one place ratline runs something an operator named rather than something
			// from its own registry: an editor is by definition their choice. Still argv,
			// never a shell string, so an EDITOR containing a semicolon is an argument
			// rather than a second command.
			//nolint:forbidigo // an editor is the operator's own choice, and this is argv
			ed := exec.CommandContext(cmd.Context(), editor, scratch)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return rlerr.Wrap(err, rlerr.CodeExternal, "%s exited without saving", editor)
			}

			edited, err := os.ReadFile(scratch)
			if err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "reading back %s", scratch)
			}
			if string(edited) == string(original) {
				g.Println("No changes.")
				return nil
			}
			if err := config.Check(edited); err != nil {
				return rlerr.Wrap(err, rlerr.CodePrecondition,
					"the edited configuration would not load, so %s was left alone", path)
			}
			if g.DryRun {
				g.Log.Info("would write the edited configuration", "path", path)
				return nil
			}
			if err := system.WriteFileAtomic(path, edited, 0o644,
				system.KeepUnchanged, system.KeepUnchanged); err != nil {
				return err
			}
			g.Printf("Wrote %s.\n", path)
			return nil
		},
	}
	return Mutating(cmd)
}

// configEdit applies a change to the file, validates it, and rolls back if it broke.
//
// The staged-verify-commit shape used everywhere else in ratline. A configuration file is
// the worst thing to leave broken, because the failure arrives on the next unrelated
// command rather than on the one that caused it.
func (g *Globals) configEdit(cmd *cobra.Command, key string, apply func([]byte) ([]byte, error), what string) error {
	path := g.configPath()
	original, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
		}
		// No file yet: start from the commented reference rather than from nothing, so
		// the operator ends up with the documented file either way.
		original = config.DefaultYAML()
		if _, err := config.Seed(path); err != nil {
			return err
		}
	}

	updated, err := apply(original)
	if err != nil {
		return err
	}
	if string(updated) == string(original) {
		g.Printf("%s is already that.\n", key)
		return nil
	}
	if err := config.Check(updated); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition,
			"that change would produce a configuration that does not load, so %s is unchanged", path)
	}
	if g.DryRun {
		g.Log.Info("would change the configuration", "setting", key, "path", path)
		return nil
	}
	if err := system.WriteFileAtomic(path, updated, 0o644,
		system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}

	if g.JSON {
		return g.EmitJSON(map[string]any{"setting": key, "changed": true, "path": path})
	}
	g.Printf("%s\n", what)
	// Read on every invocation, so there is genuinely nothing to reload — worth saying,
	// because the instinct after changing a config file is to restart something.
	g.Printf("\nRead on the next command; nothing needs reloading.\n")
	return nil
}

// configFileValue reads one setting straight from the file rather than from the loaded
// struct, which is how "set in the file" is told apart from "the default applies".
func (g *Globals) configFileValue(key string) (string, bool, error) {
	body, err := os.ReadFile(g.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", g.configPath())
	}
	return config.GetValue(body, key)
}

// isBoolSetting reports whether the shipped default for a key is a boolean, so a value an
// operator types as "yes" can be normalised rather than written as the string "yes".
func isBoolSetting(key string) bool {
	v, ok, err := config.GetValue(config.DefaultYAML(), key)
	if err != nil || !ok {
		return false
	}
	return v == "true" || v == "false"
}

func unknownSetting(key string) error {
	e := rlerr.Usagef("there is no setting called %q", key)
	if near := nearestSetting(key); near != "" {
		return e.WithHint("did you mean %s?", near)
	}
	return e.WithHint("'ratline config show' lists every setting, and " +
		"'ratline config reference' explains each one")
}

// nearestSetting finds a plausible correction, the same way `explain` does for a topic.
func nearestSetting(key string) string {
	key = strings.ToLower(key)
	keys := config.KnownKeys()
	for _, k := range keys {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	// A leaf name given without its section is the commonest mistake — "email" for
	// "acme.email" — so that is tried before anything fuzzier.
	for _, k := range keys {
		if leaf := k[strings.LastIndex(k, ".")+1:]; strings.EqualFold(leaf, key) {
			return k
		}
	}
	for _, k := range keys {
		if strings.Contains(strings.ToLower(k), key) {
			return k
		}
	}
	// A missing or extra underscore is the commonest typo — paths.systemdir for
	// paths.systemd_dir, which is the very example the configuration reference uses when
	// explaining why an unknown key is an error rather than ignored. Comparing with the
	// separators removed catches it.
	squashed := squash(key)
	for _, k := range keys {
		if squash(k) == squashed {
			return k
		}
	}
	for _, k := range keys {
		if leaf := k[strings.LastIndex(k, ".")+1:]; squash(leaf) == squashed {
			return k
		}
	}
	// Last resort: the nearest key within two edits. `explain` deliberately does not do
	// this for topics, because the realistic mistakes there are abbreviations that prefix
	// matching catches. A setting name is different — paths.systemdir for
	// paths.systemd_dir is one dropped character, and nothing above finds it.
	best, bestDist := "", 3
	for _, k := range keys {
		if d := editDistance(squash(k), squashed); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is Levenshtein, bounded by the length of the inputs, which are setting
// names rather than documents.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = minOf(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func minOf(vs ...int) int {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// squash removes the separators a name is easy to misplace, for comparison only.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r != '_' && r != '-' && r != '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func completeConfigKeys(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys := config.KnownKeys()
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v, _, _ := config.GetValue(config.DefaultYAML(), k)
		if config.IsSecret(k) {
			v = config.Redacted
		}
		out = append(out, k+"\tdefault: "+unquote(v))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// unquote strips the quoting the file uses, for display.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
