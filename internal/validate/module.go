package validate

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// appModuleRe matches the WSGI/ASGI import path form, app.main:app.
//
// This string reaches both a gunicorn command line and a systemd unit file, so
// it is pinned to identifier characters and a single colon.
var appModuleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$`)

var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// AppModule validates a Python import path plus callable.
func AppModule(s string) error {
	if s == "" {
		return rlerr.Usagef("the application module is empty").
			WithHint("pass the import path of your WSGI or ASGI callable, for example --app-module app.main:app")
	}
	if len(s) > 255 {
		return rlerr.Usagef("the application module %q is longer than 255 characters", s)
	}
	if !appModuleRe.MatchString(s) {
		return rlerr.Usagef("invalid application module %q", s).
			WithHint("the form is module.path:callable, for example app.main:app or myproject.wsgi:application")
	}
	modulePart, callable, _ := strings.Cut(s, ":")
	if strings.HasPrefix(modulePart, ".") || strings.HasSuffix(modulePart, ".") {
		return rlerr.Usagef("invalid application module %q: the module path must not start or end with a dot", s)
	}
	for _, seg := range strings.Split(modulePart, ".") {
		if !identifierRe.MatchString(seg) {
			return rlerr.Usagef("invalid application module %q: %q is not a valid Python identifier", s, seg)
		}
	}
	if !identifierRe.MatchString(callable) {
		return rlerr.Usagef("invalid application module %q: %q is not a valid Python identifier", s, callable)
	}
	return nil
}

var nodeEntryExts = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}

// bunEntryExts is the Node set plus the two extensions only Bun can execute.
//
// Bun's transpiler runs on the way in, so `bun app.tsx` is an ordinary way to start a
// server. Node cannot do that — its type stripping does not handle JSX — so the two sets
// are kept apart rather than widened into one. A .tsx entry point accepted for a node site
// would be a unit that fails on first start with a syntax error from deep inside the
// module loader, which is a long way from the flag that caused it.
var bunEntryExts = map[string]bool{".jsx": true, ".tsx": true}

// NodeEntry validates a Node entry point relative to the application directory.
func NodeEntry(s string) error { return entryPoint(s, false) }

// BunEntry validates a Bun entry point. Everything NodeEntry accepts, plus JSX.
func BunEntry(s string) error { return entryPoint(s, true) }

func entryPoint(s string, bun bool) error {
	if s == "" {
		return rlerr.Usagef("the entry point is empty").
			WithHint("pass the file that starts your server, for example --entry server.js")
	}
	dir, file := filepath.Split(s)
	if dir != "" {
		// RuntimeSubdir, not Subdir: the interpreter executes this file, nginx never
		// serves it, so a leading dot is ordinary. .next/standalone/server.js is the
		// Next.js entry point.
		if err := RuntimeSubdir(strings.TrimSuffix(dir, "/")); err != nil {
			return err
		}
	}
	if !runtimeSegmentRe.MatchString(file) {
		return rlerr.Usagef("invalid entry point %q", s).
			WithHint("give a path relative to the application directory, for example server.js or dist/main.js")
	}
	ext := strings.ToLower(filepath.Ext(file))
	if nodeEntryExts[ext] || (bun && bunEntryExts[ext]) {
		return nil
	}
	if bun {
		return rlerr.Usagef("invalid entry point %q: expected a .js, .mjs, .cjs, .ts, .jsx or .tsx file", s)
	}
	return rlerr.Usagef("invalid entry point %q: expected a .js, .mjs, .cjs or .ts file", s)
}

var packageManagers = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true}

// PackageManager validates the --package-manager choice.
func PackageManager(s string) error {
	if !packageManagers[s] {
		return rlerr.Usagef("unknown package manager %q", s).
			WithHint("choose one of npm, pnpm, yarn or bun")
	}
	return nil
}

var runtimeNames = map[string]bool{"static": true, "node": true, "bun": true, "python": true}

// RuntimeName validates the --runtime choice.
func RuntimeName(s string) error {
	if !runtimeNames[s] {
		return rlerr.Usagef("unknown runtime %q", s).
			WithHint("choose one of static, node, bun or python")
	}
	return nil
}

var (
	nodeVersionRe   = regexp.MustCompile(`^(\d{1,3})(\.\d{1,3}){0,2}$`)
	pythonVersionRe = regexp.MustCompile(`^3\.(\d{1,2})(\.\d{1,2})?$`)
)

// NodeVersion accepts a major version ("22") or a full one ("22.11.0").
func NodeVersion(s string) error {
	s = strings.TrimPrefix(s, "v")
	if !nodeVersionRe.MatchString(s) {
		return rlerr.Usagef("invalid Node version %q", s).
			WithHint("pass a major version such as 22, or a full version such as 22.11.0")
	}
	return nil
}

// BunVersion accepts "1", "1.2" or a full "1.2.21".
//
// Same shape as a Node version, and deliberately its own function rather than an alias:
// the value becomes a path component under /opt/ratline/runtimes/bun and a tag in a
// download URL, and the two runtimes' numbering has no reason to stay in step.
func BunVersion(s string) error {
	s = strings.TrimPrefix(s, "v")
	if !nodeVersionRe.MatchString(s) {
		return rlerr.Usagef("invalid Bun version %q", s).
			WithHint("pass a major or minor version such as 1.2, or a full version such as 1.2.21")
	}
	return nil
}

var packageVersionRe = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]{0,63}$`)

// PackageVersion checks a version passed through to a package manager.
//
// The leading digit is the point: it stops a value that starts with '-' from
// reaching npm as a flag rather than as a version, which would change the command
// instead of pinning it.
func PackageVersion(s string) error {
	if !packageVersionRe.MatchString(s) {
		return rlerr.Usagef("invalid version %q", s).
			WithHint("a version starts with a digit, e.g. 5.4.2")
	}
	return nil
}

// PythonVersion accepts 3.x or 3.x.y. Python 2 is not supported.
func PythonVersion(s string) error {
	if !pythonVersionRe.MatchString(s) {
		return rlerr.Usagef("invalid Python version %q", s).
			WithHint("pass a version such as 3.12, or a full version such as 3.12.4")
	}
	return nil
}

var unitBaseRe = regexp.MustCompile(`^[A-Za-z0-9:_.\\@-]+$`)

// SystemdUnitName checks a unit filename before it is written or passed to
// systemctl. Slug derivation lives in slug.go; this is the guard applied to a
// name that arrived from state or from the filesystem.
func SystemdUnitName(s string) error {
	if s == "" {
		return rlerr.Usagef("the unit name is empty")
	}
	if len(s) > 255 {
		return rlerr.Usagef("the unit name %q is longer than 255 characters", s)
	}
	if !strings.HasSuffix(s, ".service") && !strings.HasSuffix(s, ".timer") &&
		!strings.HasSuffix(s, ".socket") && !strings.HasSuffix(s, ".target") {
		return rlerr.Usagef("invalid unit name %q: it needs a .service, .timer, .socket or .target suffix", s)
	}
	if !unitBaseRe.MatchString(strings.TrimSuffix(s, filepath.Ext(s))) {
		return rlerr.Usagef("invalid unit name %q: it contains a character systemd does not accept", s)
	}
	return nil
}
