package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// notifyWriter closes found the first time needle appears in what has been
// written to it, so a test can observe *when* output arrives, not just what.
type notifyWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	needle string
	once   sync.Once
	found  chan struct{}
}

func (w *notifyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.needle) {
		w.once.Do(func() { close(w.found) })
	}
	return len(p), nil
}

func tailPath(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"/usr/bin/tail", "/bin/tail"} {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			return c
		}
	}
	t.Skip("tail not found on this host")
	return ""
}

// Following a log must show lines as they are written, not when the viewer
// finally exits. tail -f never exits on its own, so the only way this test can
// see the line is if execInPlace streams it while the child is still running.
func TestExecInPlaceStreamsBeforeTheChildExits(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(logFile, []byte("the line already in the log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &notifyWriter{needle: "the line already in the log", found: make(chan struct{})}
	g := &Globals{
		Stdout: w,
		Stderr: &bytes.Buffer{},
		Bins:   system.NewBinaries(),
	}
	g.Runner = system.NewRunner(g.Bins, log.Discard(), false)

	tail := tailPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- execInPlace(ctx, g, tail, []string{"-n", "5", "-f", logFile})
	}()

	select {
	case <-w.found:
		// The line arrived while tail was still following.
	case err := <-done:
		t.Fatalf("execInPlace returned (err=%v) without ever streaming the line", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the line was in the file before tail started, and nothing reached stdout in 5s: following does not stream")
	}

	// The operator ending a --follow is the normal way a viewer stops.
	cancel()
	if err := <-done; err != nil {
		t.Errorf("a follow ended by cancellation reported an error: %v", err)
	}
}
