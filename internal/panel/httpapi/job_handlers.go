package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
)

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request, c *Caller) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	list, err := s.Store.ListJobs(r.Context(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, list)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request, c *Caller) {
	job, err := s.Store.FindJob(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, job)
}

// handleJobStream sends a job's transcript over server-sent events.
//
// SSE rather than a WebSocket: this is one-directional text from server to browser,
// which is exactly what SSE is, and it needs no protocol upgrade, no framing library
// and no special handling in the nginx configuration beyond turning off buffering.
// A WebSocket would be a dependency and a second thing to get right for no gain.
//
// A finished job is served as one event and closed, so a browser reconnecting after
// the work ended gets the whole transcript rather than an empty stream.
func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request, c *Caller) {
	id := r.PathValue("id")
	job, err := s.Store.FindJob(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	flusher, streamable := w.(http.Flusher)
	if !streamable {
		s.fail(w, errNotStreamable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// nginx buffers proxied responses by default, which for a stream means the
	// browser sees nothing until it ends. The generated vhost turns buffering off
	// for this path; this header says so again for any other proxy in between.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// One data: line per line of output, because that is what the event-stream
	// format requires — an embedded newline would end the event early and the rest
	// would be read as a field name.
	send := func(event, data string) {
		for _, line := range strings.Split(data, "\n") {
			// Not HTML. The content type is text/event-stream, the browser hands
			// each line to an EventSource listener as a string, and nothing here
			// reaches innerHTML — the transcript is rendered into a <pre>.
			//nolint:gosec // G705: not an HTML response; see above
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, line)
		}
		flusher.Flush()
	}

	if job.Terminal() {
		send("log", strings.TrimRight(job.Output, "\n"))
		send("state", job.State)
		return
	}

	backlog, lines, live := s.Jobs.Subscribe(id)
	if !live {
		// It finished between the lookup and the subscription. Re-reading is the
		// answer rather than an empty stream that never ends.
		if fresh, err := s.Store.FindJob(r.Context(), id); err == nil {
			send("log", strings.TrimRight(fresh.Output, "\n"))
			send("state", fresh.State)
		}
		return
	}
	defer s.Jobs.Unsubscribe(id, lines)

	if backlog != "" {
		send("log", strings.TrimRight(backlog, "\n"))
	}

	// A heartbeat, so a proxy with an idle timeout does not decide a quiet build
	// is a dead connection.
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case line, open := <-lines:
			if !open {
				if fresh, err := s.Store.FindJob(r.Context(), id); err == nil {
					send("state", fresh.State)
				}
				return
			}
			// The manager marks the end of a job with a NUL-prefixed state rather
			// than a line of output, so a build that happens to print the word
			// "failed" is not mistaken for one.
			if strings.HasPrefix(line, "\x00") {
				send("state", strings.TrimPrefix(line, "\x00"))
				continue
			}
			send("log", line)
		}
	}
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, c *Caller) {
	f := store.ActionFilter{
		Action:     r.URL.Query().Get("action"),
		Target:     r.URL.Query().Get("target"),
		FailedOnly: r.URL.Query().Get("failed") == "true",
		Limit:      100,
	}
	// An admin sees the whole trail; there is no per-account privacy here, and
	// pretending otherwise in a tool where everybody is an administrator of the
	// same server would be theatre.
	if raw := r.URL.Query().Get("before"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			f.Before = n
		}
	}
	list, err := s.Store.ListActions(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, list)
}

// errNotStreamable is the one case a job stream cannot be served: a ResponseWriter
// that cannot flush, which in practice means a middleware wrapper that forgot to pass
// Flush through.
var errNotStreamable = errors.New("this connection cannot stream")
