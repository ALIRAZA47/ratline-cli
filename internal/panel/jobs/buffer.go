package jobs

import (
	"fmt"
	"strings"
	"sync"
)

// buffer collects a job's output, line by line, up to a limit.
//
// Bounded on purpose. `npm ci` on a large project prints tens of thousands of lines,
// and the useful part of a failed build is the end of it — so once the limit is
// reached the *oldest* output is dropped and a marker records that it happened. A
// transcript that silently stops at a megabyte would make a later failure look like a
// hang at exactly the moment somebody is trying to read why it broke.
type buffer struct {
	mu      sync.Mutex
	limit   int
	lines   []string
	size    int
	dropped int
	partial string
	onLine  func(string)
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	text := b.partial + string(p)
	var complete []string
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(text[:i], "\r")
		text = text[i+1:]
		b.append(line)
		complete = append(complete, line)
	}
	b.partial = text
	b.mu.Unlock()

	// Outside the lock: a subscriber's channel is buffered but publishing still
	// touches the manager's own mutex, and holding two locks in one order here and
	// the other order there is how a deadlock is written.
	if b.onLine != nil {
		for _, line := range complete {
			b.onLine(line)
		}
	}
	return len(p), nil
}

// append adds a line, evicting the oldest until it fits.
func (b *buffer) append(line string) {
	b.lines = append(b.lines, line)
	b.size += len(line) + 1
	for b.size > b.limit && len(b.lines) > 1 {
		b.size -= len(b.lines[0]) + 1
		b.lines = b.lines[1:]
		b.dropped++
	}
}

// String returns the transcript, with a note if anything was dropped.
func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	if b.dropped > 0 {
		fmt.Fprintf(&sb, "[… %d earlier line(s) dropped: the transcript is capped]\n", b.dropped)
	}
	for _, l := range b.lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	if b.partial != "" {
		sb.WriteString(b.partial)
	}
	return sb.String()
}
