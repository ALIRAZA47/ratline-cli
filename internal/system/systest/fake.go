// Package systest provides test doubles for the system package.
//
// It lives outside package system so that the production binary carries no test
// scaffolding, and so any package's tests can script external commands.
package systest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Call records one invocation.
type Call struct {
	Key     string
	Name    string
	Path    string
	Args    []string
	Dir     string
	As      string
	Mutates bool
	Stdin   string
}

// Response is what a scripted command returns.
type Response struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// FakeRunner is a system.Runner that answers from a script and records
// everything it was asked to do.
//
// Responses are matched by the command key — the logical binary name followed by
// its arguments — first exactly, then by longest prefix, so a test can pin
// "systemctl is-active x" precisely while leaving "systemctl" broadly stubbed.
type FakeRunner struct {
	mu        sync.Mutex
	calls     []Call
	responses map[string]Response

	// Default answers anything unscripted. The zero value succeeds silently.
	Default Response
	// Strict makes an unscripted command an error, which is what you want when
	// asserting that a code path runs exactly the commands you expect.
	Strict bool
	// Hook, when set, overrides everything: use it for stateful fakes.
	Hook func(system.Cmd) (*system.Result, error)
}

// NewFakeRunner returns an empty fake.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{responses: map[string]Response{}}
}

// Expect scripts a response for a command key such as "nginx -t".
func (f *FakeRunner) Expect(key string, r Response) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.responses == nil {
		f.responses = map[string]Response{}
	}
	f.responses[key] = r
	return f
}

// ExpectOutput scripts a successful response with the given stdout.
func (f *FakeRunner) ExpectOutput(key, stdout string) *FakeRunner {
	return f.Expect(key, Response{Stdout: stdout})
}

// ExpectFailure scripts a non-zero exit.
func (f *FakeRunner) ExpectFailure(key string, exitCode int, stderr string) *FakeRunner {
	return f.Expect(key, Response{ExitCode: exitCode, Stderr: stderr})
}

// Run implements system.Runner.
func (f *FakeRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	if f.Hook != nil {
		return f.Hook(c)
	}
	key := Key(c)
	call := Call{
		Key:     key,
		Name:    c.Name,
		Path:    c.Path,
		Args:    append([]string(nil), c.Args...),
		Dir:     c.Dir,
		Mutates: c.Mutates,
	}
	if c.As != nil {
		call.As = c.As.Name
	}

	f.mu.Lock()
	f.calls = append(f.calls, call)
	resp, matched := f.lookupLocked(key)
	strict := f.Strict
	f.mu.Unlock()

	if !matched && strict {
		return nil, rlerr.Genericf("FakeRunner: no response scripted for %q", key)
	}
	if resp.Err != nil {
		return nil, resp.Err
	}
	res := &system.Result{
		Path:     commandPath(c),
		Args:     c.Args,
		ExitCode: resp.ExitCode,
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
	}
	if resp.ExitCode != 0 {
		for _, ok := range c.OKExit {
			if ok == resp.ExitCode {
				return res, nil
			}
		}
		return res, rlerr.Externalf("%s failed (exit %d): %s", key, resp.ExitCode, strings.TrimSpace(resp.Stderr))
	}
	return res, nil
}

func (f *FakeRunner) lookupLocked(key string) (Response, bool) {
	if r, ok := f.responses[key]; ok {
		return r, true
	}
	// Longest matching prefix wins, so a specific script beats a general one.
	best, bestLen, found := Response{}, -1, false
	for k, r := range f.responses {
		if strings.HasPrefix(key, k) && len(k) > bestLen {
			best, bestLen, found = r, len(k), true
		}
	}
	if found {
		return best, true
	}
	return f.Default, false
}

// Calls returns every recorded invocation.
func (f *FakeRunner) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Keys returns the recorded command keys, for a compact assertion.
func (f *FakeRunner) Keys() []string {
	out := []string{}
	for _, c := range f.Calls() {
		out = append(out, c.Key)
	}
	return out
}

// Called reports whether a command matching the key prefix ran.
func (f *FakeRunner) Called(keyPrefix string) bool {
	for _, c := range f.Calls() {
		if strings.HasPrefix(c.Key, keyPrefix) {
			return true
		}
	}
	return false
}

// CountCalls counts invocations matching the key prefix.
func (f *FakeRunner) CountCalls(keyPrefix string) int {
	n := 0
	for _, c := range f.Calls() {
		if strings.HasPrefix(c.Key, keyPrefix) {
			n++
		}
	}
	return n
}

// Reset clears the recorded calls but keeps the script.
func (f *FakeRunner) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// ScriptedKeys lists the scripted keys, sorted, for failure messages.
func (f *FakeRunner) ScriptedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.responses))
	for k := range f.responses {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Key renders the match key for a command.
func Key(c system.Cmd) string {
	name := c.Name
	if name == "" {
		name = c.Path
	}
	if len(c.Args) == 0 {
		return name
	}
	return name + " " + strings.Join(c.Args, " ")
}

func commandPath(c system.Cmd) string {
	if c.Path != "" {
		return c.Path
	}
	return fmt.Sprintf("/usr/bin/%s", c.Name)
}

// Binaries returns a registry in which every known binary resolves to a fake
// absolute path, so tests need no real /usr/sbin on the host.
func Binaries() *system.Binaries {
	b := system.NewBinaries()
	for _, n := range b.Names() {
		b.Set(n, "/usr/bin/"+n)
	}
	return b
}
