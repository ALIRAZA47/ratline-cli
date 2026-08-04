package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		Short:   "Install and select managed Node and Python versions",
		GroupID: GroupRuntimes,
		Long: "Managed interpreters live under /opt/ratline/runtimes and are invoked by absolute\n" +
			"path from each unit's ExecStart.\n\n" +
			"That is the point: nvm, pyenv and shell profiles are never involved, because\n" +
			"systemd does not read them. A unit that depended on them would work when you\n" +
			"tested it by hand and fail on the next boot.",
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
						used := s.NodeVersion
						if spec.kind == "python" {
							used = s.PythonVersion
						}
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
					"node_default": g.Cfg.Runtimes.NodeDefault, "python_default": g.Cfg.Runtimes.PythonDefault})
			}
			if len(rows) == 0 {
				g.Println("No managed runtimes installed.")
				g.Println("\n  ratline runtime install node 22")
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

func newRuntimeInstallCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <node|python> <version>",
		Short: "Install a managed interpreter into /opt/ratline/runtimes",
		Args:  cobra.ExactArgs(2),
		Example: "  ratline runtime install node 22\n" +
			"  ratline runtime install python 3.12",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, version := args[0], args[1]
			switch kind {
			case "node":
				return g.installNode(cmd.Context(), version)
			case "python":
				return g.installPython(cmd.Context(), version)
			default:
				return rlerr.Usagef("unknown runtime %q", kind).WithHint("choose node or python")
			}
		},
	}
	return Mutating(cmd)
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
	want, err := fetchNodeChecksum(ctx, base+"/SHASUMS256.txt", name)
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
		Name: "tar", Args: []string{"--extract", "--xz", "--strip-components", "1",
			"--file", archive, "--directory", staging},
		Mutates: true, Timeout: 10 * time.Minute, Label: "extract",
	}); err != nil {
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

func fetchNodeChecksum(ctx context.Context, url, filename string) (string, error) {
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
		if _, err := system.EnsureDir(filepath.Join(target, "bin"), 0o755, 0, 0); err != nil {
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
		Use:   "default <node|python> <version>",
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
				return rlerr.Usagef("unknown runtime %q", kind).WithHint("choose node or python")
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
