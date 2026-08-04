package system

import (
	"context"
	"net"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// DialTimeout is the default patience for a local probe.
const DialTimeout = 5 * time.Second

// ProbeTCP reports whether something is accepting connections at addr.
func ProbeTCP(ctx context.Context, addr string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DialTimeout
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "nothing is listening at %s", addr)
	}
	return conn.Close()
}

// ProbeUnix reports whether a Unix socket is accepting connections.
//
// Existence on disk is not enough: a socket file left behind by a crashed process
// is still there, and connecting is the only way to tell the difference.
func ProbeUnix(ctx context.Context, path string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DialTimeout
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "nothing is accepting connections on %s", path)
	}
	return conn.Close()
}

// PortFree reports whether a TCP port can be bound on the loopback and on all
// interfaces.
//
// Both are checked because a service bound only to 127.0.0.1 leaves the wildcard
// bind available and vice versa; allocating a port that something else already
// half-holds produces a site that starts once and then never again.
func PortFree(port int) bool {
	for _, addr := range []string{"127.0.0.1", "0.0.0.0"} {
		l, err := net.Listen("tcp", net.JoinHostPort(addr, itoa(port)))
		if err != nil {
			return false
		}
		if err := l.Close(); err != nil {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
