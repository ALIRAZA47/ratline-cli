package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// shortTempDir is a directory whose path fits a unix socket address, which on macOS
// is 104 bytes; t.TempDir's paths do not.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rlp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole reason the socket exists. allow_from is judged on the forwarded address,
// and every tenant on the host can connect to the loopback port and write that
// header. A request on the socket is nginx's, so its header is believed; the same
// request on the port is anybody's, so it is not.
func TestForwardedHeadersAreBelievedOnlyOnTheProxySocket(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "p.sock")
	port := freePort(t)
	h := newHarness(t, func(c *panel.Config) {
		c.Listen.Address = "127.0.0.1"
		c.Listen.Port = port
		c.Listen.Socket = sock
		c.Listen.TrustProxy = false
		c.Security.AllowFrom = []string{"203.0.113.9"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.server.Serve(ctx) }()
	waitFor(t, "the socket", func() bool {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})

	viaSocket := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return net.Dial("unix", sock) },
	}}
	viaPort := &http.Client{}
	get := func(client *http.Client, url string, forwarded string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := get(viaSocket, "http://panel/api/bootstrap", "203.0.113.9"); code != http.StatusOK {
		t.Errorf("an allowed address forwarded by nginx over the socket got %d, want 200", code)
	}
	if code := get(viaSocket, "http://panel/api/bootstrap", "198.51.100.7"); code != http.StatusForbidden {
		t.Errorf("a refused address forwarded over the socket got %d, want 403", code)
	}
	tcp := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/bootstrap"
	if code := get(viaPort, tcp, "203.0.113.9"); code != http.StatusForbidden {
		t.Errorf("a tenant on the loopback port claiming an allowed address got %d, want 403", code)
	}

	// The socket is nobody's but root's and the proxy group's.
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm()&0o007 != 0 {
		t.Errorf("socket mode is %v; others must have no access", fi.Mode())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket was left behind after shutdown: %v", err)
	}
}

// The operator who has their own proxy on the TCP port can still say so; it is a
// choice they make, not a default they inherit.
func TestForwardedHeadersOnThePortNeedTrustProxy(t *testing.T) {
	port := freePort(t)
	h := newHarness(t, func(c *panel.Config) {
		c.Listen.Address = "127.0.0.1"
		c.Listen.Port = port
		c.Listen.Socket = ""
		c.Listen.TrustProxy = true
		c.Security.AllowFrom = []string{"203.0.113.9"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.server.Serve(ctx) }()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	waitFor(t, "the port", func() bool {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/bootstrap", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("with trust_proxy on, a forwarded allowed address got %d, want 200", resp.StatusCode)
	}
	cancel()
	<-done
}
