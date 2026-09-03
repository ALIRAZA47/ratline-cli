package jobs

import (
	"strings"
	"testing"
)

func TestBufferSplitsOnLinesAndPublishesEachOne(t *testing.T) {
	var got []string
	b := &buffer{limit: 4096, onLine: func(l string) { got = append(got, l) }}

	// Written in three writes, one of which splits a line in half — which is what
	// a pipe from a child process actually does.
	for _, chunk := range []string{"first line\nsec", "ond line\n", "third line\n"} {
		if _, err := b.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"first line", "second line", "third line"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("published %v, want %v", got, want)
	}
	if b.String() != "first line\nsecond line\nthird line\n" {
		t.Errorf("transcript = %q", b.String())
	}
}

// Output that arrives without a trailing newline must still be in the transcript.
// A build that dies mid-line would otherwise lose the line that says why.
func TestBufferKeepsAnUnterminatedLastLine(t *testing.T) {
	b := &buffer{limit: 4096}
	if _, err := b.Write([]byte("done\nsegmentation fault")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "segmentation fault") {
		t.Errorf("the unterminated last line was dropped: %q", b.String())
	}
}

// The cap drops the *oldest* output, because the useful part of a failed build is
// the end of it — and it says so, rather than stopping silently at the limit.
func TestBufferDropsTheOldestOutputAndSaysSo(t *testing.T) {
	b := &buffer{limit: 64}
	for i := 0; i < 100; i++ {
		if _, err := b.Write([]byte("line-with-some-length\n")); err != nil {
			t.Fatal(err)
		}
	}
	out := b.String()
	if len(out) > 400 {
		t.Errorf("the transcript grew to %d bytes despite a 64-byte cap", len(out))
	}
	if !strings.Contains(out, "dropped") {
		t.Errorf("the transcript does not say that output was dropped: %q", out)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "line-with-some-length") {
		t.Errorf("the newest line is not at the end: %q", out)
	}
	// The negative case: under the cap, nothing is dropped and nothing is claimed
	// to have been.
	small := &buffer{limit: 4096}
	if _, err := small.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(small.String(), "dropped") {
		t.Error("an uncapped transcript claims output was dropped")
	}
}

// A subscriber that is not reading must not stall the job producing the output.
func TestPublishDoesNotBlockOnASlowSubscriber(t *testing.T) {
	m := New(nil, nil, nil, 4096, 10)
	slow := make(chan string) // unbuffered and nobody reading
	m.subs["job"] = []chan string{slow}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			m.publish("job", "a line")
		}
		close(done)
	}()
	<-done // would hang for ever if publish blocked
}
