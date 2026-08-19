package cli

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

func newRuntimeCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runtime",
		Short:   "Install and select managed Node, Bun and Python versions",
		GroupID: GroupRuntimes,
		Long: "Managed interpreters live under /opt/ratline/runtimes and are invoked by absolute\n" +
			"path from each unit's ExecStart.\n\n" +
			"That is the point: nvm, pyenv, `bun upgrade` and shell profiles are never\n" +
			"involved, because systemd does not read them. A unit that depended on them\n" +
			"would work when you tested it by hand and fail on the next boot.",
	}
	cmd.AddCommand(
		newRuntimeListCommand(g),
		newRuntimeInstallCommand(g),
		newRuntimeDefaultCommand(g),
	)
	return cmd
}

func newRuntimeListCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed versions and which sites use each",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			sites, err := st.ListSites(cmd.Context(), state.SiteFilter{})
			if err != nil {
				return err
			}

			type row struct {
				Runtime string   `json:"runtime"`
				Version string   `json:"version"`
				Path    string   `json:"path"`
				Default bool     `json:"default"`
				Sites   []string `json:"sites,omitempty"`
			}
			var rows []row
			for _, spec := range []struct{ kind, def string }{
				{"node", g.Cfg.Runtimes.NodeDefault},
				{"bun", g.Cfg.Runtimes.BunDefault},
				{"python", g.Cfg.Runtimes.PythonDefault},
			} {
				dir := filepath.Join(g.Cfg.Paths.RuntimesDir, spec.kind)
				for _, version := range listRuntimeVersions(dir) {
					r := row{
						Runtime: spec.kind,
						Version: version,
						Path:    filepath.Join(dir, version),
						Default: version == spec.def,
					}
					for _, s := range sites {
						used := pinnedVersion(s, spec.kind)
						// A site with no pinned version follows the default, so it
						// belongs in that row.
						if used == version || (used == "" && r.Default && s.Runtime == spec.kind) {
							r.Sites = append(r.Sites, s.Domain)
						}
					}
					rows = append(rows, r)
				}
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"runtimes": rows,
					"node_default": g.Cfg.Runtimes.NodeDefault, "bun_default": g.Cfg.Runtimes.BunDefault,
					"python_default": g.Cfg.Runtimes.PythonDefault})
			}
			if len(rows) == 0 {
				g.Println("No managed runtimes installed.")
				g.Println("\n  ratline runtime install node 22")
				g.Println("  ratline runtime install bun 1.2")
				g.Println("  ratline runtime install python 3.12")
				return nil
			}
			tbl := g.Table("runtime", "version", "default", "sites", "path")
			for _, r := range rows {
				tbl.Row(r.Runtime, r.Version, yesNo(r.Default), fmt.Sprint(len(r.Sites)), r.Path)
			}
			return tbl.Render()
		},
	}
}

// pinnedVersion is the version a site pins for one kind of runtime, or "" when it
// pins none and therefore follows the configured default.
func pinnedVersion(s *state.Site, kind string) string {
	switch kind {
	case "node":
		return s.NodeVersion
	case "bun":
		return s.BunVersion
	case "python":
		return s.PythonVersion
	}
	return ""
}

func newRuntimeInstallCommand(g *Globals) *cobra.Command {
	var (
		withPM2    bool
		pm2Version string
		baseline   bool
	)
	cmd := &cobra.Command{
		Use:   "install <node|bun|python> <version>",
		Short: "Install a managed interpreter into /opt/ratline/runtimes",
		Args:  cobra.ExactArgs(2),
		Example: "  ratline runtime install node 22 --with-pm2\n" +
			"  ratline runtime install bun 1.2\n" +
			"  ratline runtime install python 3.12",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, version := args[0], args[1]
			if kind != "node" && (withPM2 || pm2Version != "") {
				return rlerr.Usagef("--with-pm2 applies to node, not %s", kind).
					WithHint("PM2 is a node supervisor; a bun site runs directly under systemd")
			}
			if kind != "bun" && baseline {
				return rlerr.Usagef("--baseline applies to bun, not %s", kind)
			}
			switch kind {
			case "node":
				if err := g.installNode(cmd.Context(), version); err != nil {
					return err
				}
				if withPM2 || pm2Version != "" {
					return g.installPM2(cmd.Context(), version, pm2Version)
				}
				return nil
			case "bun":
				return g.installBun(cmd.Context(), version, baseline)
			case "python":
				return g.installPython(cmd.Context(), version)
			default:
				return rlerr.Usagef("unknown runtime %q", kind).WithHint("choose node, bun or python")
			}
		},
	}
	f := cmd.Flags()
	f.BoolVar(&withPM2, "with-pm2", false,
		"node: also install PM2, which is what a node site is supervised by unless --daemon direct is used")
	f.StringVar(&pm2Version, "pm2-version", "", "node: pin PM2 to this version rather than the latest")
	f.BoolVar(&baseline, "baseline", false,
		"bun: force the build for x86-64 CPUs without AVX2 (detected from /proc/cpuinfo by default)")
	return Mutating(cmd)
}

// installPM2 installs PM2 into one managed Node tree.
//
// Per Node version rather than once globally, for two reasons. A PM2 resolved
// against Node 18 is not the one a Node 22 site should be running, and a single
// shared install would mean `runtime default` silently changed the supervisor
// binary underneath every existing site.
//
// It lands in the root-owned runtime prefix, so tenants can execute it and cannot
// modify it — a writable supervisor binary in a tenant's home would be a way to run
// arbitrary code from within a service unit.
func (g *Globals) installPM2(ctx context.Context, nodeVersion, pm2Version string) error {
	if err := validate.NodeVersion(nodeVersion); err != nil {
		return err
	}
	nodeVersion = strings.TrimPrefix(nodeVersion, "v")
	prefix := filepath.Join(g.Cfg.Paths.RuntimesDir, "node", nodeVersion)
	npm := filepath.Join(prefix, "bin", "npm")
	if !system.Exists(npm) {
		return rlerr.Preconditionf("Node %s has no npm at %s", nodeVersion, npm)
	}

	spec := "pm2"
	if pm2Version != "" {
		// Validated because it is passed as an argv element to npm; a version that
		// looked like a flag would change what npm did.
		if err := validate.PackageVersion(pm2Version); err != nil {
			return err
		}
		spec = "pm2@" + pm2Version
	}

	g.Log.Info("installing PM2", "node", nodeVersion, "spec", spec, "prefix", prefix)
	if _, err := g.Runner.Run(ctx, system.Cmd{
		Path: npm,
		// --global with --prefix keeps it inside this Node tree instead of /usr/local.
		Args: []string{"install", "--global", "--prefix", prefix, "--no-audit", "--no-fund", spec},
		Env: []string{
			"PATH=" + filepath.Join(prefix, "bin") + ":" + system.DefaultPath,
			"HOME=" + prefix,
			"npm_config_update_notifier=false",
		},
		Mutates: true, Stream: true,
		Timeout: g.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "install pm2",
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "installing PM2 failed").
			WithHint("sites can run without it: ratline site add ... --daemon direct")
	}

	pm2 := filepath.Join(prefix, "bin", "pm2")
	if !system.Exists(pm2) {
		return rlerr.Preconditionf("npm reported success but there is no %s", pm2)
	}
	// npm ran under the provisioning umask, so everything it wrote is 0750/0640 and
	// the tenant that has to exec pm2 cannot. The tarball extraction fixes its own
	// tree, but PM2 lands afterwards — and a unit that cannot exec its supervisor
	// fails with 203/EXEC and a "Permission denied" that says nothing about umasks.
	if err := makeWorldExecutable(prefix); err != nil {
		return err
	}
	res, err := g.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"--version"},
		Env: []string{"PATH=" + filepath.Join(prefix, "bin") + ":" + system.DefaultPath, "HOME=" + prefix},
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the installed PM2 does not run")
	}
	installed := strings.TrimSpace(res.Out())

	if g.JSON {
		return g.EmitJSON(map[string]any{"pm2": installed, "node": nodeVersion, "path": pm2})
	}
	g.Printf("Installed PM2 %s for Node %s at %s\n", installed, nodeVersion, pm2)
	g.Printf("\nNode sites on this version now reload without dropping requests:\n" +
		"  ratline site reload <domain>\n")
	return nil
}

// installNode downloads an official tarball and verifies its checksum.
//
// The checksum is not optional: an unverified interpreter downloaded over the
// network and then run as root on every site start is a supply-chain hole, and
// nodejs.org publishes SHASUMS256.txt for exactly this.
func (g *Globals) installNode(ctx context.Context, version string) error {
	if err := validate.NodeVersion(version); err != nil {
		return err
	}
	version = strings.TrimPrefix(version, "v")

	target := filepath.Join(g.Cfg.Paths.RuntimesDir, "node", version)
	if system.Exists(filepath.Join(target, "bin", "node")) {
		g.Printf("Node %s is already installed at %s\n", version, target)
		return nil
	}

	full, err := g.resolveNodeVersion(ctx, version)
	if err != nil {
		return err
	}
	arch := nodeArch()
	if arch == "" {
		return rlerr.Preconditionf("no official Node build for this architecture")
	}
	name := fmt.Sprintf("node-v%s-linux-%s.tar.xz", full, arch)
	base := strings.TrimRight(g.Cfg.Runtimes.NodeMirror, "/") + "/v" + full
	tarURL := base + "/" + name

	if g.DryRun {
		g.Log.Info("would install Node", "version", full, "url", tarURL, "into", target)
		return nil
	}

	g.Log.Info("downloading Node", "version", full, "url", tarURL)
	tmpDir, err := os.MkdirTemp("", "ratline-node-*")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating a temporary directory")
	}
	defer os.RemoveAll(tmpDir)

	archive := filepath.Join(tmpDir, name)
	sum, err := download(ctx, tarURL, archive, 30*time.Minute)
	if err != nil {
		return err
	}
	want, err := fetchChecksum(ctx, base+"/SHASUMS256.txt", name)
	if err != nil {
		return err
	}
	if sum != want {
		return rlerr.Preconditionf("the downloaded Node archive does not match its published checksum").
			WithField("expected", want).WithField("got", sum).
			WithHint("this could be a corrupted download or a tampered mirror; nothing was installed")
	}
	g.Log.Info("checksum verified", "sha256", sum[:16]+"…")

	if _, err := system.EnsureDir(filepath.Dir(target), 0o755, 0, 0); err != nil {
		return err
	}
	// Extracted into a staging directory then renamed, so a failed extraction
	// never leaves a half-populated version that sites would then try to use.
	staging := target + ".incoming"
	_ = os.RemoveAll(staging)
	if _, err := system.EnsureDir(staging, 0o755, 0, 0); err != nil {
		return err
	}
	if _, err := g.Runner.Run(ctx, system.Cmd{
		// --no-same-owner and --no-same-permissions, because tar running as root
		// otherwise restores whatever uid, gid and mode the archive was built with.
		// The official Node tarballs carry a uid that does not exist here, which left
		// the interpreter every site executes owned by a phantom account — and would
		// hand it to a real user the day that uid was allocated. --no-same-permissions
		// applies the umask instead, so the modes are set explicitly below.
		Name: "tar", Args: []string{"--extract", "--xz", "--strip-components", "1",
			"--no-same-owner", "--no-same-permissions",
			"--file", archive, "--directory", staging},
		Mutates: true, Timeout: 10 * time.Minute, Label: "extract",
	}); err != nil {
		os.RemoveAll(staging)
		return err
	}
	// A managed interpreter is executed by every tenant, so the tree has to be
	// traversable and executable by all of them and writable by none. The
	// provisioning umask would otherwise leave it at 0750/0640.
	if err := makeWorldExecutable(staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if !system.Exists(filepath.Join(staging, "bin", "node")) {
		os.RemoveAll(staging)
		return rlerr.Preconditionf("the archive did not contain bin/node")
	}
	if err := os.Rename(staging, target); err != nil {
		os.RemoveAll(staging)
		return rlerr.Wrap(err, rlerr.CodeGeneric, "moving the runtime into place")
	}

	res, err := g.Runner.Run(ctx, system.Cmd{Path: filepath.Join(target, "bin", "node"), Args: []string{"--version"}})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the installed Node does not run")
	}
	installed := strings.TrimSpace(res.Out())

	// The first runtime installed becomes the default, since an operator who
	// installs exactly one version means that one.
	if g.Cfg.Runtimes.NodeDefault == "" {
		g.Cfg.Runtimes.NodeDefault = version
		if err := g.Cfg.Save(g.configPath()); err != nil {
			g.Log.Warn("could not record the default version", "err", err)
		} else {
			g.Log.Info("set as the default Node version", "version", version)
		}
	}

	if g.JSON {
		return g.EmitJSON(map[string]any{"runtime": "node", "version": installed, "path": target})
	}
	g.Printf("Installed Node %s at %s\n", installed, target)
	g.Printf("\nUse it for a site:\n  ratline site add app.example.com --user <user> --runtime node --entry server.js --node %s\n", version)
	return nil
}

// installBun downloads an official release asset and verifies its checksum.
//
// Same rule as Node's, for the same reason: an unverified interpreter downloaded over
// the network and then executed by every request to a site is a supply-chain hole. Bun
// publishes SHASUMS256.txt beside each release asset.
//
// Deliberately not `curl -fsSL bun.sh/install | bash`. That script installs into
// ~/.bun for whoever ran it, puts the binary somewhere `bun upgrade` can rewrite, and
// depends on a shell profile to be found — three things a systemd unit cannot rely on.
func (g *Globals) installBun(ctx context.Context, version string, baseline bool) error {
	if err := validate.BunVersion(version); err != nil {
		return err
	}
	version = strings.TrimPrefix(version, "v")

	arch, err := bunArch(baseline)
	if err != nil {
		return err
	}

	full, err := g.resolveBunVersion(ctx, version)
	if err != nil {
		return err
	}
	// The directory is named by what was asked for, not by what it resolved to, so
	// `--bun 1.2` keeps finding the tree that `runtime install bun 1.2` created. The
	// exact build is reported below and by `bun --version`.
	target := filepath.Join(g.Cfg.Paths.RuntimesDir, "bun", version)
	if system.Exists(filepath.Join(target, "bin", "bun")) {
		g.Printf("Bun %s is already installed at %s\n", version, target)
		return nil
	}

	name := "bun-linux-" + arch + ".zip"
	base := strings.TrimRight(g.Cfg.Runtimes.BunMirror, "/") + "/download/bun-v" + full
	zipURL := base + "/" + name

	if g.DryRun {
		g.Log.Info("would install Bun", "version", full, "url", zipURL, "into", target)
		return nil
	}

	g.Log.Info("downloading Bun", "version", full, "arch", arch, "url", zipURL)
	tmpDir, err := os.MkdirTemp("", "ratline-bun-*")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating a temporary directory")
	}
	defer os.RemoveAll(tmpDir)

	archive := filepath.Join(tmpDir, name)
	sum, err := download(ctx, zipURL, archive, 30*time.Minute)
	if err != nil {
		return err
	}
	want, err := fetchChecksum(ctx, base+"/SHASUMS256.txt", name)
	if err != nil {
		return err
	}
	if sum != want {
		return rlerr.Preconditionf("the downloaded Bun archive does not match its published checksum").
			WithField("expected", want).WithField("got", sum).
			WithHint("this could be a corrupted download or a tampered mirror; nothing was installed")
	}
	g.Log.Info("checksum verified", "sha256", sum[:16]+"…")

	if _, err := system.EnsureDir(filepath.Dir(target), 0o755, 0, 0); err != nil {
		return err
	}
	// Staged then renamed, so a failed extraction never leaves a half-populated version
	// that sites would then try to use.
	staging := target + ".incoming"
	_ = os.RemoveAll(staging)
	// EnsureDir creates a single level, so the staging directory has to exist before its
	// bin subdirectory — creating bin/ alone fails on the missing parent.
	if _, err := system.EnsureDir(staging, 0o755, 0, 0); err != nil {
		return err
	}
	if _, err := system.EnsureDir(filepath.Join(staging, "bin"), 0o755, 0, 0); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := extractBunBinary(archive, filepath.Join(staging, "bin", "bun")); err != nil {
		os.RemoveAll(staging)
		return err
	}
	// bunx is how a project's binaries are run, and bun ships it as a link to itself
	// rather than as a second executable in the archive.
	if _, err := system.EnsureSymlink("bun", filepath.Join(staging, "bin", "bunx")); err != nil {
		os.RemoveAll(staging)
		return err
	}
	// A managed interpreter is executed by every tenant, so the tree has to be
	// traversable and executable by all of them and writable by none.
	if err := makeWorldExecutable(staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		os.RemoveAll(staging)
		return rlerr.Wrap(err, rlerr.CodeGeneric, "moving the runtime into place")
	}

	bunBin := filepath.Join(target, "bin", "bun")
	res, err := g.Runner.Run(ctx, system.Cmd{Path: bunBin, Args: []string{"--version"},
		Env: []string{"PATH=" + system.DefaultPath, "HOME=" + target}})
	if err != nil {
		wrapped := rlerr.Wrap(err, rlerr.CodePrecondition, "the installed Bun does not run")
		// The overwhelmingly common cause on an older VPS: the default x64 build
		// needs AVX2 and the CPU does not have it, which the kernel reports as
		// SIGILL. That reads as a corrupt download unless someone says otherwise.
		if arch == "x64" {
			return wrapped.WithHint("if this was an illegal instruction, this CPU has no AVX2 and "+
				"needs the baseline build:\n"+
				"        rm -rf %s\n"+
				"        ratline runtime install bun %s --baseline", target, version)
		}
		return wrapped
	}
	installed := strings.TrimSpace(res.Out())

	// The first runtime installed becomes the default, since an operator who installs
	// exactly one version means that one.
	if g.Cfg.Runtimes.BunDefault == "" {
		g.Cfg.Runtimes.BunDefault = version
		if err := g.Cfg.Save(g.configPath()); err != nil {
			g.Log.Warn("could not record the default version", "err", err)
		} else {
			g.Log.Info("set as the default Bun version", "version", version)
		}
	}

	if g.JSON {
		return g.EmitJSON(map[string]any{"runtime": "bun", "version": installed,
			"release": full, "arch": arch, "path": target})
	}
	g.Printf("Installed Bun %s at %s\n", installed, target)
	g.Printf("\nUse it for a site:\n  ratline site add app.example.com --user <user> --runtime bun --entry server.ts --bun %s\n", version)
	return nil
}

// extractBunBinary pulls the one executable out of a Bun release zip.
//
// Go's archive/zip rather than the unzip binary: a minimal Ubuntu does not ship unzip,
// and adding a dependency on it to install a runtime would turn a missing package into
// a provisioning failure. It also means the archive's own names never reach the
// filesystem — the member is matched by base name and written to a path ratline chose,
// so no crafted entry can escape the destination directory.
func extractBunBinary(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "reading the Bun archive")
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != "bun" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeExternal, "opening bun inside the archive")
		}
		defer rc.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0o755)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", dest)
		}
		defer out.Close()
		// Bounded: the archive says how big the member is, but that number comes from
		// the file being unpacked, so the copy is capped independently.
		if _, err := io.Copy(out, io.LimitReader(rc, 512<<20)); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "extracting bun")
		}
		return out.Close()
	}
	return rlerr.Preconditionf("the archive did not contain a bun executable")
}

// resolveBunVersion turns a partial version into the newest release that starts with it.
//
// GitHub's releases feed rather than its API: it lives on the same host as the download,
// so one mirror setting covers both, and it needs no token. The feed only carries recent
// releases, so an older line has to be named in full — which is said plainly rather than
// silently installing whatever the newest release happens to be.
func (g *Globals) resolveBunVersion(ctx context.Context, version string) (string, error) {
	if strings.Count(version, ".") == 2 {
		return version, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := strings.TrimRight(g.Cfg.Runtimes.BunMirror, "/") + ".atom"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", "ratline")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "fetching the Bun release feed").
			WithHint("check outbound network access, or pass a full version such as %s.0", version)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "reading the Bun release feed")
	}
	// The feed is newest first, so the first match is the latest.
	for _, m := range bunTagRe.FindAllStringSubmatch(string(body), -1) {
		found := m[1]
		if found == version || strings.HasPrefix(found, version+".") {
			return found, nil
		}
	}
	return "", rlerr.Preconditionf("no recent Bun release matches %s", version).
		WithHint("the release feed only carries recent versions; pass a full one such as %s.0, "+
			"or check https://bun.sh/releases", version)
}

// bunTagRe matches the release tags Bun publishes, bun-v1.2.21.
var bunTagRe = regexp.MustCompile(`bun-v(\d{1,3}\.\d{1,3}\.\d{1,3})`)

// bunArch maps this host's architecture onto the name in Bun's asset filenames.
//
// The x64 baseline build exists because Bun's default x86-64 build requires AVX2, which
// a good number of older VPS hosts do not expose. Getting that wrong is not a graceful
// failure — the process dies on an illegal instruction with no message of its own — so
// the CPU is asked rather than assumed.
func bunArch(baseline bool) (string, error) {
	switch goruntime.GOARCH {
	case "arm64":
		if baseline {
			return "", rlerr.Usagef("--baseline is an x86-64 build; this host is arm64")
		}
		return "aarch64", nil
	case "amd64":
		if baseline || !hasAVX2() {
			return "x64-baseline", nil
		}
		return "x64", nil
	default:
		return "", rlerr.Preconditionf("Bun publishes no build for %s", goruntime.GOARCH).
			WithHint("Bun supports x86-64 and arm64 on Linux; use --runtime node here")
	}
}

// hasAVX2 reports whether this CPU advertises AVX2.
//
// Unreadable /proc/cpuinfo is treated as "has it": that is the ordinary build, and the
// post-install `bun --version` check catches the mistake with a hint rather than
// silently installing the slower baseline everywhere the file is hidden.
func hasAVX2() bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		flags, ok := strings.CutPrefix(line, "flags")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(flags) {
			if f == "avx2" {
				return true
			}
		}
		return false
	}
	return true
}

// makeWorldExecutable gives a managed runtime tree the modes every tenant needs.
//
// Root-owned, world-readable, world-traversable, and writable by nobody else. A
// directory or binary a tenant could modify would be a way to run arbitrary code
// inside every service unit on the box, and one they cannot traverse is an
// interpreter no site can start.
func makeWorldExecutable(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Lchown(path, 0, 0); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "setting the owner of %s", path)
		}
		// A symlink's own mode is not meaningful and chmod would follow it.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o644)
		if info.IsDir() || info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		return system.Chmod(path, mode)
	})
}

// resolveNodeVersion turns a major version into the latest full version.
func (g *Globals) resolveNodeVersion(ctx context.Context, version string) (string, error) {
	if strings.Count(version, ".") == 2 {
		return version, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := strings.TrimRight(g.Cfg.Runtimes.NodeMirror, "/") + "/index.tab"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "fetching the Node version index").
			WithHint("check outbound network access, or pass a full version such as %s.11.0", version)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "reading the Node version index")
	}
	prefix := "v" + version + "."
	// The index is newest first, so the first match is the latest.
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], prefix) {
			return strings.TrimPrefix(fields[0], "v"), nil
		}
	}
	return "", rlerr.Preconditionf("no Node release found for version %s", version).
		WithHint("check the major version; 18, 20, 22 and 24 are current")
}

func fetchChecksum(ctx context.Context, url, filename string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building a request")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "fetching the checksum file").
			WithHint("ratline will not install an unverified interpreter")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", rlerr.Externalf("the checksum file returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "reading the checksum file")
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	return "", rlerr.Preconditionf("no checksum published for %s", filename)
}

// download streams a URL to disk and returns its SHA-256.
func download(ctx context.Context, url, dest string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", "ratline")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "downloading %s", url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", dest)
	}
	defer f.Close()
	h := sha256.New()
	// Hash while writing, so the file is never read twice and a huge tarball does
	// not have to fit in memory.
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20)); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "downloading %s", url)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// nodeArch maps this host's architecture onto the name nodejs.org uses in its
// tarball filenames.
func nodeArch() string {
	switch goruntime.GOARCH {
	case "arm64":
		return "arm64"
	case "amd64":
		return "x64"
	case "arm":
		return "armv7l"
	case "ppc64le":
		return "ppc64le"
	case "s390x":
		return "s390x"
	default:
		return ""
	}
}

// installPython uses the distribution or deadsnakes, and refuses to guess.
//
// Building CPython from source takes fifteen minutes and pulls in a dozen -dev
// packages. Doing that silently inside a provisioning command would be a nasty
// surprise, so this reports the exact commands instead when no package is
// available.
func (g *Globals) installPython(ctx context.Context, version string) error {
	if err := validate.PythonVersion(version); err != nil {
		return err
	}
	target := filepath.Join(g.Cfg.Paths.RuntimesDir, "python", version)
	if system.Exists(filepath.Join(target, "bin", "python3")) {
		g.Printf("Python %s is already managed at %s\n", version, target)
		return nil
	}

	// A version already on the system is the best answer: it gets security
	// updates from the distribution.
	for _, candidate := range []string{"/usr/bin/python" + version, "/usr/local/bin/python" + version} {
		if !system.Exists(candidate) {
			continue
		}
		if g.DryRun {
			g.Log.Info("would link the system Python", "from", candidate, "to", target)
			return nil
		}
		// An interpreter that cannot make a virtualenv is not a runtime ratline can use.
		//
		// Debian and Ubuntu ship python3.12 and put venv in python3.12-venv, so the
		// interpreter is present and `python -m venv` fails. Adopting it anyway reported
		// "Python 3.12 is available … and is now managed", and the failure surfaced three
		// commands later out of `site add`, which then rolled the whole site back. The
		// apt path below already installs the venv package; this is the path that
		// adopted an interpreter without checking it.
		if err := g.ensurePythonVenv(ctx, candidate, version); err != nil {
			return err
		}
		// MkdirAllMode rather than EnsureDir: EnsureDir is a single level, and on a
		// fresh box neither .../python nor .../python/<version> exists yet, so it
		// failed with ENOENT on the very first `runtime install python`.
		if err := system.MkdirAllMode(filepath.Join(target, "bin"), 0o755); err != nil {
			return err
		}
		// A symlink rather than a copy: the distribution keeps patching the real
		// interpreter, and a copy would silently stop receiving those fixes.
		if _, err := system.EnsureSymlink(candidate, filepath.Join(target, "bin", "python3")); err != nil {
			return err
		}
		if g.Cfg.Runtimes.PythonDefault == "" {
			g.Cfg.Runtimes.PythonDefault = version
			if err := g.Cfg.Save(g.configPath()); err != nil {
				g.Log.Warn("could not record the default version", "err", err)
			}
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"runtime": "python", "version": version,
				"path": target, "source": "system", "interpreter": candidate})
		}
		g.Printf("Python %s is available at %s, and is now managed at %s\n", version, candidate, target)
		return nil
	}

	if !g.Bins.Available("apt-get") {
		return rlerr.Preconditionf("Python %s is not installed and this host does not use apt", version).
			WithHint("install it however this distribution does, then re-run this command")
	}

	// Try the distribution's own package before adding a third-party repository.
	pkg := "python" + version
	g.Log.Info("looking for a distribution package", "package", pkg)
	if _, err := g.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"install", "-y", pkg, pkg + "-venv", pkg + "-dev"},
		Mutates: true, Stream: true, Timeout: g.Cfg.Runtimes.InstallTimeout.D(), Label: "apt-get install",
	}); err != nil {
		return rlerr.Preconditionf("Python %s is not available from this distribution", version).
			WithHint("Ubuntu users can add the deadsnakes archive, which publishes every version:\n"+
				"        add-apt-repository -y ppa:deadsnakes/ppa && apt-get update\n"+
				"        ratline runtime install python %s\n"+
				"      ratline will not add a third-party repository on your behalf", version)
	}
	// The install may have landed a differently-named binary, so recurse once to
	// pick up the symlink path above.
	if system.Exists("/usr/bin/python" + version) {
		return g.installPython(ctx, version)
	}
	return rlerr.Preconditionf("the package installed but /usr/bin/python%s does not exist", version)
}

func newRuntimeDefaultCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "default <node|bun|python> <version>",
		Short: "Set the version new sites use when they do not pin one",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, version := args[0], args[1]
			dir := filepath.Join(g.Cfg.Paths.RuntimesDir, kind, version)
			switch kind {
			case "node":
				if err := validate.NodeVersion(version); err != nil {
					return err
				}
				if !system.Exists(filepath.Join(dir, "bin", "node")) {
					return rlerr.Preconditionf("Node %s is not installed", version).
						WithHint("ratline runtime install node %s", version)
				}
				g.Cfg.Runtimes.NodeDefault = version
			case "bun":
				if err := validate.BunVersion(version); err != nil {
					return err
				}
				if !system.Exists(filepath.Join(dir, "bin", "bun")) {
					return rlerr.Preconditionf("Bun %s is not installed", version).
						WithHint("ratline runtime install bun %s", version)
				}
				g.Cfg.Runtimes.BunDefault = version
			case "python":
				if err := validate.PythonVersion(version); err != nil {
					return err
				}
				if !system.Exists(filepath.Join(dir, "bin", "python3")) {
					return rlerr.Preconditionf("Python %s is not managed", version).
						WithHint("ratline runtime install python %s", version)
				}
				g.Cfg.Runtimes.PythonDefault = version
			default:
				return rlerr.Usagef("unknown runtime %q", kind).WithHint("choose node, bun or python")
			}
			if err := g.Cfg.Save(g.configPath()); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"runtime": kind, "default": version})
			}
			g.Printf("New %s sites will use %s unless they pin a version.\n", kind, version)
			g.Printf("Existing sites are unchanged; move one with:\n  ratline site runtime <domain> --%s %s\n", kind, version)
			return nil
		},
	}
	return Mutating(cmd)
}

// ensurePythonVenv checks that an interpreter can actually create a virtualenv, and
// installs the distribution's venv package if it cannot.
//
// `python -m venv` is the only thing ratline uses a Python runtime for, so an interpreter
// without it is not usable however present it looks. Checked with ensurepip rather than by
// creating a throwaway environment: it is the piece Debian splits out, and importing it
// costs milliseconds where building a venv costs seconds on every runtime install.
func (g *Globals) ensurePythonVenv(ctx context.Context, interpreter, version string) error {
	works := func() bool {
		_, err := g.Runner.Run(ctx, system.Cmd{
			Path: interpreter, Args: []string{"-c", "import ensurepip, venv"},
			Label: "check python venv support",
		})
		return err == nil
	}
	if works() {
		return nil
	}

	pkg := "python" + version + "-venv"
	if !g.Bins.Available("apt-get") {
		return rlerr.Preconditionf("%s cannot create a virtualenv", interpreter).
			WithHint("its venv module is missing; install this distribution's equivalent of "+
				"%s and re-run this command", pkg)
	}
	g.Log.Info("the interpreter cannot create a virtualenv; installing the package that provides it",
		"package", pkg)
	if _, err := g.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"install", "-y", pkg},
		Mutates: true, Stream: true,
		Timeout: g.Cfg.Runtimes.InstallTimeout.D(), Label: "apt-get install " + pkg,
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "installing %s failed", pkg).
			WithHint("ratline needs 'python -m venv' to work; install %s by hand and re-run", pkg)
	}
	if !works() {
		return rlerr.Preconditionf("%s still cannot create a virtualenv after installing %s",
			interpreter, pkg).
			WithHint("check 'python%s -m venv --help' by hand", version)
	}
	return nil
}
