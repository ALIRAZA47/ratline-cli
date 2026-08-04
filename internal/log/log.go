// Package log provides ratline's logger and its audit trail.
//
// Human-readable logs always go to stderr so that stdout stays parseable for
// --json consumers. With --json the same records are emitted as JSON lines on
// stderr, which keeps machine consumers from having to scrape prose.
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
)

// Level aliases slog's level so callers need not import slog.
type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// ParseLevel converts a config string into a level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", s)
}

// Options configures a Logger.
type Options struct {
	Out   io.Writer // defaults to os.Stderr
	Level Level
	JSON  bool
	Color bool
}

// Logger is a thin, CLI-shaped wrapper over slog.
type Logger struct {
	s     *slog.Logger
	out   io.Writer
	level Level
	color bool
	json  bool
}

// New builds a Logger. Callers decide colour; this package never inspects the
// terminal itself.
func New(o Options) *Logger {
	if o.Out == nil {
		o.Out = os.Stderr
	}
	var h slog.Handler
	if o.JSON {
		h = slog.NewJSONHandler(o.Out, &slog.HandlerOptions{Level: o.Level})
	} else {
		h = newHumanHandler(o.Out, o.Level, o.Color)
	}
	return &Logger{s: slog.New(h), out: o.Out, level: o.Level, color: o.Color, json: o.JSON}
}

// Discard returns a logger that swallows everything, for tests.
func Discard() *Logger { return New(Options{Out: io.Discard, Level: LevelError + 1}) }

func (l *Logger) Debug(msg string, kv ...any) { l.s.Debug(msg, kv...) }
func (l *Logger) Info(msg string, kv ...any)  { l.s.Info(msg, kv...) }
func (l *Logger) Warn(msg string, kv ...any)  { l.s.Warn(msg, kv...) }
func (l *Logger) Error(msg string, kv ...any) { l.s.Error(msg, kv...) }

// With returns a logger that adds the given attributes to every record.
func (l *Logger) With(kv ...any) *Logger {
	c := *l
	c.s = l.s.With(kv...)
	return &c
}

// Enabled reports whether records at this level are emitted.
func (l *Logger) Enabled(level Level) bool { return level >= l.level }

// Level reports the configured threshold.
func (l *Logger) Level() Level { return l.level }

// Out returns the underlying writer (stderr in production).
func (l *Logger) Out() io.Writer { return l.out }

// Color reports whether ANSI colour is in use.
func (l *Logger) Color() bool { return l.color }

// JSON reports whether records are emitted as JSON.
func (l *Logger) JSON() bool { return l.json }

// Stream returns a writer that logs each complete line it receives at the
// given level, tagged with prefix. Used to surface the output of long-running
// children (npm ci, pip install, certbot) as it happens.
//
// The caller must Close it to flush a trailing partial line.
func (l *Logger) Stream(level Level, prefix string) io.WriteCloser {
	return &lineWriter{
		max: 64 << 10,
		emit: func(line string) {
			if prefix == "" {
				l.s.Log(context.Background(), level, line)
			} else {
				l.s.Log(context.Background(), level, line, "source", prefix)
			}
		},
	}
}

// lineWriter buffers writes and emits one callback per newline-terminated
// line, so interleaved child output stays readable.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	max  int
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for {
		i := indexByte(p, '\n')
		if i < 0 {
			break
		}
		w.buf = append(w.buf, p[:i]...)
		w.flushLocked()
		p = p[i+1:]
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max { // a child emitting one enormous line must not eat memory
		w.flushLocked()
	}
	return n, nil
}

func (w *lineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return nil
}

func (w *lineWriter) flushLocked() {
	line := strings.TrimRight(string(w.buf), "\r")
	w.buf = w.buf[:0]
	if line == "" {
		return
	}
	w.emit(line)
}

func indexByte(p []byte, b byte) int {
	for i := range p {
		if p[i] == b {
			return i
		}
	}
	return -1
}

// ANSI escapes, used only when the caller has confirmed a colour-capable tty.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
)

// humanHandler renders records as compact CLI lines rather than logfmt.
type humanHandler struct {
	w     io.Writer
	mu    *sync.Mutex
	level Level
	color bool
	attrs []slog.Attr
}

func newHumanHandler(w io.Writer, level Level, color bool) *humanHandler {
	return &humanHandler{w: w, mu: &sync.Mutex{}, level: level, color: color}
}

func (h *humanHandler) Enabled(_ context.Context, l Level) bool { return l >= h.level }

func (h *humanHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	sigil, colour := h.decorate(r.Level)
	if colour != "" {
		b.WriteString(colour)
	}
	b.WriteString(sigil)
	if colour != "" {
		b.WriteString(ansiReset)
	}
	b.WriteByte(' ')
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		h.writeAttr(&b, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		h.writeAttr(&b, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *humanHandler) decorate(l Level) (sigil, colour string) {
	switch {
	case l >= LevelError:
		return "✗", pick(h.color, ansiRed)
	case l >= LevelWarn:
		return "!", pick(h.color, ansiYellow)
	case l >= LevelInfo:
		return "→", ""
	default:
		return "·", pick(h.color, ansiDim)
	}
}

func pick(on bool, s string) string {
	if on {
		return s
	}
	return ""
}

func (h *humanHandler) writeAttr(b *strings.Builder, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Key == "" {
		return
	}
	v := a.Value.String()
	if v == "" || strings.ContainsAny(v, " \t\"'") {
		v = fmt.Sprintf("%q", v)
	}
	b.WriteByte(' ')
	if h.color {
		b.WriteString(ansiDim)
	}
	b.WriteString(a.Key)
	b.WriteByte('=')
	b.WriteString(v)
	if h.color {
		b.WriteString(ansiReset)
	}
}

func (h *humanHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(slices.Clip(h.attrs), as...)
	return &c
}

// WithGroup is a no-op: ratline's records are flat by design.
func (h *humanHandler) WithGroup(string) slog.Handler { return h }
