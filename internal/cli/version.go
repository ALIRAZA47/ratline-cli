package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// VersionInfo is what `ratline version` reports. It exists to make a bug report
// useful without a follow-up question: the binary, the host, and every moving
// part ratline depends on.
type VersionInfo struct {
	Version   string   `json:"version"`
	Commit    string   `json:"commit"`
	BuildDate string   `json:"build_date"`
	Go        string   `json:"go"`
	Platform  string   `json:"platform"`
	OS        string   `json:"os"`
	Kernel    string   `json:"kernel,omitempty"`
	Supported bool     `json:"os_supported"`
	Nginx     string   `json:"nginx,omitempty"`
	Certbot   string   `json:"certbot,omitempty"`
	OpenSSH   string   `json:"openssh,omitempty"`
	Systemd   string   `json:"systemd,omitempty"`
	Node      []string `json:"node_runtimes,omitempty"`
	Bun       []string `json:"bun_runtimes,omitempty"`
	Python    []string `json:"python_runtimes,omitempty"`
	Config    string   `json:"config"`
	ConfigOK  bool     `json:"config_loaded"`
}

func newVersionCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Print the version, the host and the available runtimes",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := g.versionInfo(cmd.Context())
			if g.JSON {
				return g.EmitJSON(info)
			}
			g.Println(buildinfo.Full())
			pairs := [][2]string{
				{"os", info.OS},
				{"config", info.Config + configSuffix(info.ConfigOK)},
			}
			if info.Kernel != "" {
				pairs = append(pairs, [2]string{"kernel", info.Kernel})
			}
			pairs = append(pairs,
				[2]string{"nginx", orNotInstalled(info.Nginx)},
				[2]string{"certbot", orNotInstalled(info.Certbot)},
				[2]string{"openssh", orNotInstalled(info.OpenSSH)},
				[2]string{"systemd", orNotInstalled(info.Systemd)},
				[2]string{"node runtimes", orNone(info.Node)},
				[2]string{"bun runtimes", orNone(info.Bun)},
				[2]string{"python runtimes", orNone(info.Python)},
			)
			return g.Fields(pairs...)
		},
	}
	return NonRoot(cmd)
}

var versionNumberRe = regexp.MustCompile(`[0-9]+(\.[0-9]+)+[a-z0-9.\-+~]*`)

func (g *Globals) versionInfo(ctx context.Context) VersionInfo {
	info := VersionInfo{
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		BuildDate: buildinfo.Date,
		Go:        buildinfo.GoVersion(),
		Platform:  buildinfo.Platform(),
		OS:        g.OS.PrettyName,
		Kernel:    g.OS.Kernel,
		Supported: g.OS.Supported(),
		Config:    g.configPath(),
	}
	if g.Cfg != nil {
		info.ConfigOK = g.Cfg.Loaded
		info.Node = listRuntimeVersions(filepath.Join(g.Cfg.Paths.RuntimesDir, "node"))
		info.Bun = listRuntimeVersions(filepath.Join(g.Cfg.Paths.RuntimesDir, "bun"))
		info.Python = listRuntimeVersions(filepath.Join(g.Cfg.Paths.RuntimesDir, "python"))
	}
	// Version probes are best-effort: a server without certbot yet is a normal
	// state, not an error, and `version` must never fail because of one.
	info.Nginx = g.probeVersion(ctx, "nginx", "-v")
	info.Certbot = g.probeVersion(ctx, "certbot", "--version")
	info.OpenSSH = g.probeVersion(ctx, "sshd", "-V")
	info.Systemd = g.probeVersion(ctx, "systemctl", "--version")
	return info
}

func (g *Globals) probeVersion(ctx context.Context, bin string, args ...string) string {
	if g.Bins == nil || g.Runner == nil || !g.Bins.Available(bin) {
		return ""
	}
	// nginx -v and sshd -V write to stderr and exit non-zero; that is a
	// successful probe, not a failure.
	res, err := g.Runner.Run(ctx, system.Cmd{Name: bin, Args: args, OKExit: []int{1, 255}})
	if res == nil {
		return ""
	}
	if err != nil && res.Stdout == "" && res.Stderr == "" {
		return ""
	}
	out := res.Stdout
	if strings.TrimSpace(out) == "" {
		out = res.Stderr
	}
	if m := versionNumberRe.FindString(out); m != "" {
		return m
	}
	return strings.TrimSpace(firstLine(out))
}

// listRuntimeVersions reports the managed interpreter versions on disk. State is
// only an index; /opt/ratline/runtimes is the truth, so this reads the directory.
func listRuntimeVersions(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func orNotInstalled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not installed"
	}
	return s
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "none installed"
	}
	return strings.Join(v, ", ")
}

func configSuffix(loaded bool) string {
	if loaded {
		return ""
	}
	return " (not present; using built-in defaults)"
}
