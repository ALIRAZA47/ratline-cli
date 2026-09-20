// Package sitepath answers one question: where does a site's own software live?
//
// It exists because three callers need the same answer and two of them cannot ask each
// other for it. internal/runtime resolves a build command's first word and builds the
// environment for a deploy, a hook and `site exec`; internal/unit writes the PATH into a
// job's systemd unit; and `doctor` compares what a unit carries against what the site
// would be given now. runtime already imports unit, so the list could not live in either.
//
// Keeping it in one place is not tidiness. A site's `npm` is the npm of the Node version
// that site is pinned to, under /opt/ratline/runtimes, and nothing puts that on anybody's
// ambient PATH — so every layer that runs a tenant's command has to arrive at the same
// directories in the same order, or the same words mean different programs depending on
// what triggered them. A job whose PATH disagreed with `site exec`'s was exactly that:
// `--command 'npm run nightly'` failed at 3am with "npm was not found on PATH" while the
// identical command run by hand worked.
package sitepath

import (
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// RuntimeBinDirs is the bin directory of the managed interpreter a site is pinned to.
//
// A site created with --node 24 gets its node from /opt/ratline/runtimes/node/24/bin, and
// so must its npm. A site that pinned nothing takes the server's default; a site whose
// runtime is static has no interpreter and gets nothing, which is correct rather than a
// gap — nginx serves it and there is no process.
func RuntimeBinDirs(cfg *config.Config, site *state.Site) []string {
	version := ""
	switch site.Runtime {
	case "node":
		version = orDefault(site.NodeVersion, cfg.Runtimes.NodeDefault)
	case "bun":
		version = orDefault(site.BunVersion, cfg.Runtimes.BunDefault)
	case "python":
		version = orDefault(site.PythonVersion, cfg.Runtimes.PythonDefault)
	default:
		return nil
	}
	if version == "" {
		return nil
	}
	return []string{filepath.Join(cfg.Paths.RuntimesDir, site.Runtime, version, "bin")}
}

// Dirs is where a site's own programs live, in the order they are looked for.
//
// A venv or node_modules binary first, because a project that ships its own tooling means
// that tooling: `pytest`, `vite` and `prisma` are the project's, not the server's. Then the
// managed interpreter, which is where its `npm` and its `python` live. The system path is
// not here — PATH adds it behind these, and a resolver walks these and then falls back —
// so that a site can never be made to shadow a system binary for anything but itself.
//
// siteDir is passed rather than derived so that the caller's idea of where the site is and
// this one's cannot differ.
//
// A site's own long-running service is deliberately not built from this list: it puts the
// managed interpreter first, so that the process PID 1 supervises is the version ratline
// says the site runs, whatever a tenant has dropped into node_modules/.bin. This list is
// for the tenant's own commands — a build, a hook, a job, a worker, `site exec` — where
// the project's tooling is the point.
func Dirs(cfg *config.Config, site *state.Site, siteDir string) []string {
	dirs := []string{
		filepath.Join(siteDir, "venv", "bin"),
		filepath.Join(siteDir, "app", "node_modules", ".bin"),
	}
	return append(dirs, RuntimeBinDirs(cfg, site)...)
}

// PATH is what a site's software runs with: its own directories, then the system path.
//
// ratline's own directories go first and the system path last, and nothing a tenant writes
// gets to reorder that: internal/runtime drops a PATH out of the site's .env, and
// ratline-shell's exec mode ignores one for the same reason. A tenant who could set PATH
// could point their unit's `npm` at a program of their choosing — which is theirs to run
// anyway, but it would no longer be the interpreter ratline says the site uses, and the
// place that breaks is a long way from the place it was decided.
func PATH(cfg *config.Config, site *state.Site, siteDir string) string {
	return strings.Join(append(Dirs(cfg, site, siteDir), system.DefaultPath), ":")
}

func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
