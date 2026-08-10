package mongod

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// RenderConf produces /etc/mongod.conf's contents. remote selects whether mongod
// listens beyond localhost; everything else — and above all
// `security.authorization: enabled` — is fixed in the template, so there is no code
// path that renders an unauthenticated server.
func RenderConf(remote bool) ([]byte, error) {
	tmpl, err := template.ParseFS(templates.FS, "mongo/mongod.conf.tmpl")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded mongod.conf template")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ BindRemote bool }{remote}); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering mongod.conf")
	}
	return buf.Bytes(), nil
}

// writeConf stages, validates and installs the managed configuration, registering the
// undo that puts back exactly what was there. It reports whether the file changed, so
// the caller knows if a restart means anything.
//
// takeover permits replacing a file without the managed header. Only the install flow
// passes it, and only on the run where it put the package there — the pristine package
// default is the one unmanaged mongod.conf ratline may claim. Everything else follows
// the standing rule: a config ratline did not write is a config ratline does not touch.
func (m *Manager) writeConf(ctx context.Context, rb *system.Rollback, remote, takeover bool) (bool, error) {
	body, err := RenderConf(remote)
	if err != nil {
		return false, err
	}

	confPath := m.confPath()
	previous, readErr := os.ReadFile(confPath)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return false, rlerr.Wrap(readErr, rlerr.CodeGeneric, "reading %s", ConfPath)
	}
	if exists && bytes.Equal(previous, body) {
		return false, nil
	}
	if exists && !takeover && !m.confState().Managed {
		return false, rlerr.Preconditionf("%s is not managed by ratline, so ratline will not rewrite it", ConfPath).
			WithHint("this mongod was configured by hand or by another tool; change its " +
				"bindIp yourself, or reinstall through 'ratline db install' on a fresh host")
	}

	// Validated with the real parser before it lands, the same as nginx and sshd
	// configs. --outputConfig resolves the file and exits without opening the data
	// directory, so it is safe against a running server. This catches a template that
	// does not parse — the render is fixed text, but "the config we ship is valid" is
	// worth proving with mongod rather than asserting with a comment.
	staged, err := os.CreateTemp(filepath.Dir(confPath), ".mongod.conf.staged-*")
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "staging mongod.conf")
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	if _, err := staged.Write(body); err != nil {
		staged.Close()
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", stagedPath)
	}
	if err := staged.Close(); err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", stagedPath)
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name:  "mongod",
		Args:  []string{"--config", stagedPath, "--outputConfig"},
		Label: "mongod config check",
	}); err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeExternal, "mongod rejected the staged configuration")
	}

	if err := system.WriteFileAtomic(confPath, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	rb.Push("restore the previous "+ConfPath, func(ctx context.Context) error {
		if !exists {
			return os.Remove(confPath)
		}
		if err := system.WriteFileAtomic(confPath, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
		// The running process has to match the file that was put back, or the
		// "rolled back" server is still running the configuration that failed.
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"restart", ServiceName},
			Mutates: true, Label: "restart mongod",
		})
		return err
	})
	return true, nil
}

// restartAndVerify restarts mongod and proves the outcome: the server answers on the
// given connection string and enforces authorization. The proof is the point — a unit
// that restarts cleanly and a server that does what the config says are separate facts,
// and only the second one protects anybody.
func (m *Manager) restartAndVerify(ctx context.Context, uri string) (*mongo.ServerInfo, error) {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"restart", ServiceName},
		Mutates: true, Label: "restart mongod",
	}); err != nil {
		return nil, err
	}
	info, err := m.waitForServer(ctx, uri)
	if err != nil {
		return nil, err
	}
	if !info.AuthEnabled {
		return nil, rlerr.Externalf("mongod restarted but does not enforce authorization").
			WithHint("the running server was asked, not the config file — something else " +
				"decides its configuration; check `systemctl cat mongod` for command-line overrides")
	}
	return info, nil
}

// ListensRemotely asks the kernel — not the config file — whether mongod is bound
// beyond localhost. The distinction is the point: a drop-in override or a stray
// command-line flag can make the file and the process disagree, and only the process
// decides who can connect.
func (m *Manager) ListensRemotely(ctx context.Context) (bool, error) {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "ss", Args: []string{"-H", "-l", "-t", "-n"}, Label: "listening sockets",
	})
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeExternal, "listing the listening sockets")
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		for _, field := range strings.Fields(line) {
			addr, port, ok := splitHostPort(field)
			if !ok || port != Port {
				continue
			}
			switch addr {
			case "127.0.0.1", "[::1]", "::1", "localhost":
			default:
				return true, nil
			}
		}
	}
	return false, nil
}

// splitHostPort cuts "0.0.0.0:27017" or "[::]:27017" at the last colon, which is the
// one net.SplitHostPort would want brackets for.
func splitHostPort(s string) (addr, port string, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// verifyBind proves the running server's reachability matches what was just asked for,
// after a bind change and its restart.
func (m *Manager) verifyBind(ctx context.Context, wantRemote bool) error {
	remote, err := m.ListensRemotely(ctx)
	if err != nil {
		return err
	}
	if remote == wantRemote {
		return nil
	}
	if wantRemote {
		return rlerr.Externalf("the config opens the bind, but the running mongod still listens only on localhost").
			WithHint("something else decides its configuration; check `systemctl cat %s` "+
				"for command-line overrides", ServiceName)
	}
	return rlerr.Externalf("the config binds localhost only, but the running mongod still listens on other interfaces").
		WithHint("something else decides its configuration; check `systemctl cat %s` "+
			"for command-line overrides", ServiceName)
}

// waitForServer polls until mongod answers on the given connection string. A restart
// returns before the server listens, so the first attempts failing is the normal case,
// not a problem to report — unless the deadline passes, at which point the last error
// is the truth worth relaying.
func (m *Manager) waitForServer(ctx context.Context, uri string) (*mongo.ServerInfo, error) {
	deadline := time.Now().Add(m.startWait())
	db := m.db()
	var lastErr error
	for {
		info, err := db.PingURI(ctx, uri)
		if err == nil {
			return info, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, rlerr.Wrap(ctx.Err(), rlerr.CodeGeneric, "waiting for mongod to answer")
		case <-time.After(m.pollInterval()):
		}
	}
	return nil, rlerr.Wrap(lastErr, rlerr.CodeExternal,
		"mongod did not answer within %s of starting", m.startWait()).
		WithHint("journalctl -u %s -n 50 says why it is unhappy", ServiceName)
}
