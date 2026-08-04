package config

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

//go:embed defaults.yaml
var embedded embed.FS

// maxConfigBytes stops a malformed or hostile config file from being read into
// memory in full.
const maxConfigBytes = 1 << 20

var (
	defaultOnce sync.Once
	defaultCfg  Config
	defaultErr  error
)

// DefaultYAML returns the embedded, commented default file. `ratline init`
// writes it verbatim, comments included.
func DefaultYAML() []byte {
	b, err := embedded.ReadFile("defaults.yaml")
	if err != nil {
		panic("ratline: embedded defaults.yaml is missing: " + err.Error())
	}
	return b
}

// Default returns the built-in configuration.
//
// It is parsed from the same embedded YAML an operator would edit, so a default
// cannot drift away from what the reference file documents. A parse failure here
// is a build-time bug, caught by TestDefaultsParse.
func Default() *Config {
	defaultOnce.Do(func() {
		var c Config
		if err := decodeStrict(DefaultYAML(), &c); err != nil {
			defaultErr = err
			return
		}
		defaultCfg = c
	})
	if defaultErr != nil {
		panic("ratline: embedded defaults.yaml does not parse: " + defaultErr.Error())
	}
	c := defaultCfg
	return c.clone()
}

// Load reads a configuration file, starting from the defaults so a partial file
// is a valid file. Unknown keys are an error: a typo in a setting name would
// otherwise be silently ignored, which is how a server ends up not doing what
// its config says.
func Load(path string) (*Config, error) {
	c := Default()
	c.SourcePath = path

	data, err := system.ReadFileLimit(path, maxConfigBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, rlerr.Preconditionf("no configuration file at %s", path).
				WithHint("run 'ratline init' to create one")
		}
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s", path)
	}
	if err := decodeStrict(data, c); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeUsage, "%s is not valid", path).
			WithHint("YAML is whitespace-sensitive; check the indentation around the line mentioned above")
	}
	c.SourcePath = path
	c.Loaded = true
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadOrDefault loads path if it exists and falls back to the built-in defaults
// if it does not.
//
// Read-only commands must work on a server where `ratline init` has not run
// yet; a missing config is not a reason to refuse to print a version or run
// doctor. Mutating commands check Loaded and say so.
func LoadOrDefault(path string) (*Config, error) {
	c, err := Load(path)
	if err == nil {
		return c, nil
	}
	if rlerr.Is(err, rlerr.CodePrecondition) && !system.Exists(path) {
		d := Default()
		d.SourcePath = path
		d.Loaded = false
		return d, nil
	}
	return nil, err
}

// Seed writes the commented default file if it is absent, reporting whether it
// created it. An existing file is never overwritten.
func Seed(path string) (bool, error) {
	if system.Exists(path) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if _, err := system.EnsureDir(dir, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	// Ownership is left alone: ratline runs as root, so a file it creates is
	// already root-owned, and an explicit chown would fail anywhere else for no
	// benefit.
	if err := system.WriteFileAtomic(path, DefaultYAML(), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	return true, nil
}

// Save writes the configuration back out. Comments from the reference file are
// not preserved: this is used by `ratline init` and `runtime default`, which
// change values an operator set through the CLI rather than by hand.
func (c *Config) Save(path string) error {
	var buf bytes.Buffer
	buf.WriteString("# ratline configuration\n")
	buf.WriteString("# Written by ratline. The commented reference is available from 'ratline config reference'.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the configuration")
	}
	if err := enc.Close(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the configuration")
	}
	dir := filepath.Dir(path)
	if _, err := system.EnsureDir(dir, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	// 0644 on purpose: the file holds no secrets. Credentials live in
	// /etc/ratline/dns and /etc/ratline/ssh, which are 0700.
	return system.WriteFileAtomic(path, buf.Bytes(), 0o644, system.KeepUnchanged, system.KeepUnchanged)
}

// LogLevel resolves logging.level.
func (c *Config) LogLevel() log.Level {
	l, err := logLevel(c.Logging.Level)
	if err != nil {
		return log.LevelInfo
	}
	return l
}

func logLevel(s string) (log.Level, error) { return log.ParseLevel(s) }

func decodeStrict(data []byte, into *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// clone deep-copies the slices and maps so that one caller mutating its config
// cannot affect the cached defaults.
func (c *Config) clone() *Config {
	out := *c
	out.Server.PublicIPv4 = append([]string(nil), c.Server.PublicIPv4...)
	out.Server.PublicIPv6 = append([]string(nil), c.Server.PublicIPv6...)
	out.Users.Reserved = append([]string(nil), c.Users.Reserved...)
	out.SSH.AllowedAlgorithms = append([]string(nil), c.SSH.AllowedAlgorithms...)
	out.SSH.RejectedAlgorithms = append([]string(nil), c.SSH.RejectedAlgorithms...)
	if c.SSH.CommandPresets != nil {
		out.SSH.CommandPresets = make(map[string]string, len(c.SSH.CommandPresets))
		for k, v := range c.SSH.CommandPresets {
			out.SSH.CommandPresets[k] = v
		}
	}
	return &out
}

// ResolvePath expands a path relative to the config file's directory. Absolute
// paths are returned unchanged.
func (c *Config) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	base := filepath.Dir(c.SourcePath)
	if base == "" || base == "." {
		base = filepath.Dir(DefaultPath)
	}
	return filepath.Join(base, p)
}

// EnvConfigPath honours RATLINE_CONFIG for automation that cannot pass --config.
func EnvConfigPath() string { return os.Getenv("RATLINE_CONFIG") }
