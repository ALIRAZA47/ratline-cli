package panel

import (
	"strings"
	"testing"
)

// The socket is where forwarded headers are believed, so its path is written into a
// root-owned vhost and opened by root; a relative one would resolve against whatever
// the panel's working directory happened to be.
func TestTheProxySocketMustBeAnAbsolutePath(t *testing.T) {
	c := Default()
	c.Listen.Socket = "run/panel.sock"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "listen.socket") {
		t.Errorf("a relative socket path validated: %v", err)
	}
	c = Default()
	c.Listen.SocketGroup = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "socket_group") {
		t.Errorf("a socket with no group validated: %v", err)
	}
	c = Default()
	c.Listen.Socket = ""
	c.Listen.SocketGroup = ""
	if err := c.Validate(); err != nil {
		t.Errorf("no socket at all is a valid configuration: %v", err)
	}
}

// nginx is pointed at the socket when there is one, and the loopback port only
// otherwise. The trailing colon is nginx's own spelling for a unix upstream.
func TestProxyUpstreamPrefersTheSocket(t *testing.T) {
	c := Default()
	if got := c.ProxyUpstream(); got != "unix:/run/ratline-panel/panel.sock:" {
		t.Errorf("ProxyUpstream = %q", got)
	}
	c.Listen.Socket = ""
	if got := c.ProxyUpstream(); got != "127.0.0.1:8420" {
		t.Errorf("ProxyUpstream without a socket = %q", got)
	}
	c.Listen.Address = "0.0.0.0"
	if got := c.ProxyUpstream(); got != "127.0.0.1:8420" {
		t.Errorf("an unspecified bind should proxy to loopback, got %q", got)
	}
}

// Every tenant on the host can reach the loopback port, so a header arriving there
// proves nothing. Trusting it must be something an operator turns on, not something
// the default hands them.
func TestForwardedHeadersAreNotTrustedOnThePortByDefault(t *testing.T) {
	if Default().Listen.TrustProxy {
		t.Error("trust_proxy defaults to on; any tenant could then claim any address")
	}
}
