package mysqld

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// RenderConf produces the managed bind drop-in. remote selects whether the server listens
// beyond localhost; everything else is fixed. There is no rendered variant that opens the
// bind without the caller having gone through `db access`, which checks the firewall first.
func RenderConf(remote bool) []byte {
	bind := "127.0.0.1"
	note := "# Only this machine can connect. `ratline db access allow <address>` opens the\n" +
		"# port to a remote address, firewall first.\n"
	if remote {
		bind = "0.0.0.0"
		note = "# `ratline db access allow` has admitted remote addresses, so the server listens\n" +
			"# on every interface and the firewall decides who gets in. `db access list` shows who.\n"
	}
	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("#\n")
	b.WriteString("# The MySQL bind address, written by `ratline db install --engine mysql` and\n")
	b.WriteString("# rewritten by `ratline db access`. Hand edits survive only until the next of\n")
	b.WriteString("# those commands runs.\n")
	b.WriteString("[mysqld]\n")
	b.WriteString(note)
	b.WriteString("bind-address = " + bind + "\n")
	return []byte(b.String())
}

// writeConf stages, writes and registers the undo for the managed drop-in, reporting
// whether the file changed. takeover permits replacing a file without the managed header;
// only the install flow passes it, on the run where it created the server.
//
// There is no offline validator here: `mysqld --validate-config` reads the whole config
// chain, not a fragment, and a fresh drop-in is a fragment. The load-bearing check is the
// restart-and-verify that follows every call — the running server is asked whether it came
// up and enforces what was asked, which is stronger than parsing a file anyway.
func (m *Manager) writeConf(ctx context.Context, rb *system.Rollback, remote, takeover bool) (bool, error) {
	body := RenderConf(remote)
	confPath := m.confPath()

	previous, readErr := os.ReadFile(confPath)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return false, rlerr.Wrap(readErr, rlerr.CodeGeneric, "reading %s", confPath)
	}
	if exists && bytes.Equal(previous, body) {
		return false, nil
	}
	if exists && !takeover && !m.confState().Managed {
		return false, rlerr.Preconditionf("%s is not managed by ratline, so ratline will not rewrite it", m.distro().ConfDropIn).
			WithHint("this MySQL was configured by hand or by another tool; change its " +
				"bind-address yourself, or reinstall through 'ratline db install --engine mysql'")
	}
	if _, err := system.EnsureDir(filepath.Dir(confPath), 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	if err := system.WriteFileAtomic(confPath, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	rb.Push("restore the previous "+m.distro().ConfDropIn, func(ctx context.Context) error {
		if !exists {
			if err := os.Remove(confPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := system.WriteFileAtomic(confPath, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"restart", m.ServiceName()},
			Mutates: true, Label: "restart mysql",
		})
		return err
	})
	return true, nil
}

// restartAndVerify restarts the server and proves the outcome: it answers on the admin
// defaults-file (over TCP, as ratline will use it). For MySQL, reachability with valid
// credentials is the proof — the server has no unauthenticated mode to guard against.
func (m *Manager) restartAndVerify(ctx context.Context, adminDefaults string) (*mysql.ServerInfo, error) {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"restart", m.ServiceName()},
		Mutates: true, Label: "restart mysql",
	}); err != nil {
		return nil, err
	}
	return m.waitForServer(ctx, adminDefaults)
}

// waitForServer polls until the server answers on the given defaults-file. A restart
// returns before the socket is ready, so early failures are expected; the deadline is what
// turns a server that never comes up into an error.
func (m *Manager) waitForServer(ctx context.Context, defaults string) (*mysql.ServerInfo, error) {
	deadline := time.Now().Add(m.startWait())
	db := m.db()
	var lastErr error
	for {
		info, err := db.PingWith(ctx, defaults)
		if err == nil {
			return info, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, rlerr.Wrap(ctx.Err(), rlerr.CodeGeneric, "waiting for MySQL to answer")
		case <-time.After(m.pollInterval()):
		}
	}
	return nil, rlerr.Wrap(lastErr, rlerr.CodeExternal,
		"MySQL did not answer within %s of starting", m.startWait()).
		WithHint("journalctl -u %s -n 50 says why it is unhappy", m.ServiceName())
}

// ListensRemotely asks the kernel — not the config — whether the server is bound beyond
// localhost. Only the running process decides who can connect.
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

func splitHostPort(s string) (addr, port string, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// verifyBind proves the running server's reachability matches intent after a bind change.
func (m *Manager) verifyBind(ctx context.Context, wantRemote bool) error {
	remote, err := m.ListensRemotely(ctx)
	if err != nil {
		return err
	}
	if remote == wantRemote {
		return nil
	}
	if wantRemote {
		return rlerr.Externalf("the config opens the bind, but the running MySQL still listens only on localhost").
			WithHint("something else decides its configuration; check `systemctl cat %s`", m.ServiceName())
	}
	return rlerr.Externalf("the config binds localhost only, but the running MySQL still listens on other interfaces").
		WithHint("something else decides its configuration; check `systemctl cat %s`", m.ServiceName())
}
