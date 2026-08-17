package redisd

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/redis"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// RenderConf produces ratline's managed include. remote selects whether Redis listens
// beyond localhost. protected-mode stays on throughout: with a password set it does not
// block authenticated clients, and leaving it on is the belt to the firewall's braces.
func RenderConf(remote bool) []byte {
	bind := "bind 127.0.0.1 -::1"
	note := "# Only this machine can connect. `ratline db access allow` opens the port to a\n" +
		"# remote address, firewall first.\n"
	if remote {
		bind = "bind * -::*"
		note = "# `ratline db access allow` has admitted remote addresses, so Redis listens on\n" +
			"# every interface and the firewall decides who gets in.\n"
	}
	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("#\n# ratline's Redis directives, loaded last so they win. Rewritten by\n")
	b.WriteString("# `ratline db install --engine redis` and `ratline db access`.\n")
	b.WriteString(note)
	b.WriteString(bind + "\n")
	b.WriteString("protected-mode yes\n")
	b.WriteString("aclfile " + ACLFile + "\n")
	return []byte(b.String())
}

// renderACLFile is the initial aclfile: the admin (default) user with the operator's
// password and full access. ACL SAVE rewrites it (hashing the password) as users are added.
func renderACLFile(adminPassword string) []byte {
	return []byte("user default on >" + adminPassword + " ~* &* +@all\n")
}

// writeConf writes the managed include with rollback, reporting whether it changed.
func (m *Manager) writeConf(ctx context.Context, rb *system.Rollback, remote, takeover bool) (bool, error) {
	body := RenderConf(remote)
	path := m.confPath()
	previous, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return false, rlerr.Wrap(readErr, rlerr.CodeGeneric, "reading %s", ConfPath)
	}
	if exists && bytes.Equal(previous, body) {
		return false, nil
	}
	if exists && !takeover && !m.confState().Managed {
		return false, rlerr.Preconditionf("%s is not managed by ratline, so ratline will not rewrite it", ConfPath).
			WithHint("change the bind yourself, or reinstall through 'ratline db install --engine redis'")
	}
	if _, err := system.EnsureDir(filepath.Dir(path), 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return false, err
	}
	rb.Push("restore the previous "+ConfPath, func(ctx context.Context) error {
		if !exists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := system.WriteFileAtomic(path, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
		_, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"restart", ServiceName}, Mutates: true, Label: "restart redis"})
		return err
	})
	return true, nil
}

// writeACLFile writes the aclfile owned by the redis user (so the server can read it) with
// rollback. The password it carries is why it is 0640 and not world-readable.
func (m *Manager) writeACLFile(rb *system.Rollback, adminPassword string) error {
	path := m.aclFile()
	previous, readErr := os.ReadFile(path)
	existed := readErr == nil

	uid, gid := redisUIDGID()
	if err := system.WriteFileAtomic(path, renderACLFile(adminPassword), 0o640, uid, gid); err != nil {
		return err
	}
	rb.Push("restore "+ACLFile, func(context.Context) error {
		if !existed {
			return os.Remove(path)
		}
		return system.WriteFileAtomic(path, previous, 0o640, uid, gid)
	})
	return nil
}

// ensureInclude appends the include line to the stock redis.conf once, so ratline's file is
// loaded. The append is marked and reversible.
func (m *Manager) ensureInclude(rb *system.Rollback) error {
	path := m.mainConf()
	body, err := os.ReadFile(path)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s (is redis-server installed?)", MainConf)
	}
	if strings.Contains(string(body), includeLine) {
		return nil
	}
	appended := string(body)
	if !strings.HasSuffix(appended, "\n") {
		appended += "\n"
	}
	appended += "\n# " + system.ManagedHeader + " — load ratline's directives last\n" + includeLine + "\n"
	if err := system.WriteFileAtomic(path, []byte(appended), 0o640, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	rb.Push("remove the ratline include from "+MainConf, func(context.Context) error {
		return system.WriteFileAtomic(path, body, 0o640, system.KeepUnchanged, system.KeepUnchanged)
	})
	return nil
}

// restartAndVerify restarts Redis and proves it answers the admin credentials and enforces
// authentication (an unauthenticated PING is refused).
func (m *Manager) restartAndVerify(ctx context.Context, adminURI string) (*redis.ServerInfo, error) {
	if _, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"restart", ServiceName}, Mutates: true, Label: "restart redis"}); err != nil {
		return nil, err
	}
	info, err := m.waitForServer(ctx, adminURI)
	if err != nil {
		return nil, err
	}
	if !m.db().AuthEnforced(ctx, redis.DefaultHost, Port) {
		return nil, rlerr.Externalf("Redis restarted but answers an unauthenticated PING").
			WithHint("something else decides its configuration; check the include in " + MainConf)
	}
	return info, nil
}

func (m *Manager) waitForServer(ctx context.Context, adminURI string) (*redis.ServerInfo, error) {
	deadline := time.Now().Add(m.startWait())
	db := m.db()
	var lastErr error
	for {
		info, err := db.PingURI(ctx, adminURI)
		if err == nil {
			return info, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, rlerr.Wrap(ctx.Err(), rlerr.CodeGeneric, "waiting for Redis to answer")
		case <-time.After(m.pollInterval()):
		}
	}
	return nil, rlerr.Wrap(lastErr, rlerr.CodeExternal, "Redis did not answer within %s of starting", m.startWait()).
		WithHint("journalctl -u %s -n 50 says why", ServiceName)
}

// ListensRemotely asks the kernel whether Redis is bound beyond localhost.
func (m *Manager) ListensRemotely(ctx context.Context) (bool, error) {
	res, err := m.Runner.Run(ctx, system.Cmd{Name: "ss", Args: []string{"-H", "-l", "-t", "-n"}, Label: "listening sockets"})
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

func (m *Manager) verifyBind(ctx context.Context, wantRemote bool) error {
	remote, err := m.ListensRemotely(ctx)
	if err != nil {
		return err
	}
	if remote == wantRemote {
		return nil
	}
	if wantRemote {
		return rlerr.Externalf("the config opens the bind, but the running Redis still listens only on localhost").
			WithHint("check the include in %s", MainConf)
	}
	return rlerr.Externalf("the config binds localhost only, but the running Redis still listens on other interfaces").
		WithHint("check the include in %s", MainConf)
}

// redisUIDGID looks up the redis service account so the aclfile it must read is owned by
// it. If the account is not found (before the package is installed, or a nonstandard
// layout) ownership is left unchanged and the mode alone protects the file.
func redisUIDGID() (int, int) {
	u, err := user.Lookup("redis")
	if err != nil {
		return system.KeepUnchanged, system.KeepUnchanged
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid
}
