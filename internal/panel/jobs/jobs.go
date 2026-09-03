// Package jobs runs the ratline invocations that outlive a request.
//
// A deploy clones, installs, builds, restarts and health-checks; an issuance talks to
// a certificate authority. Holding an HTTP request open for that means the operation
// is lost the moment a laptop lid closes, and it means a proxy timeout looks like a
// failed deploy. So the work is a row with a transcript: the request returns a job
// id, the browser watches the log arrive, and closing the tab changes nothing.
//
// Concurrency is one by default, and that is not a limitation being apologised for.
// ratline takes a global lock for every mutation, so a second job would sit inside
// ratline waiting for the first and eventually report exit 5. Queueing here turns
// that into a position in a line somebody can see.
package jobs

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Spec is everything needed to run one job.
type Spec struct {
	ID      string
	Action  string
	Target  string
	ActorID string
	Actor   string
	Policy  rl.Policy
	Request rl.Request
}

// Manager owns the queue and the workers.
type Manager struct {
	store  *store.Store
	client *rl.Client
	log    *log.Logger

	// outputLimit bounds one transcript. A runaway build must not be able to fill
	// the disk through the panel's own database.
	outputLimit int
	retain      int

	queue   chan Spec
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	started bool

	mu   sync.Mutex
	subs map[string][]chan string
	// live holds the transcript of a running job, so a browser that arrives late
	// sees what it missed rather than starting from whatever comes next.
	live map[string]*buffer
}

// New returns a manager. Call Start before submitting.
func New(st *store.Store, client *rl.Client, lg *log.Logger, outputLimit, retain int) *Manager {
	if lg == nil {
		lg = log.Discard()
	}
	if outputLimit <= 0 {
		outputLimit = 1 << 20
	}
	return &Manager{
		store:       st,
		client:      client,
		log:         lg,
		outputLimit: outputLimit,
		retain:      retain,
		// Bounded, so a burst of submissions is refused with a clear message
		// rather than accepted into a queue that grows until the process dies.
		queue: make(chan Spec, 64),
		subs:  map[string][]chan string{},
		live:  map[string]*buffer{},
	}
}

// Start launches the workers and reconciles jobs left running by a restart.
func (m *Manager) Start(ctx context.Context, workers int) error {
	if workers < 1 {
		workers = 1
	}
	// A job is a child of this process, so a restart killed it. A row that still
	// says "running" would show a spinner for ever; saying it failed is both true
	// and the thing that makes somebody look at the server.
	if n, err := m.store.FailStrandedJobs(ctx, time.Now().UTC()); err != nil {
		return err
	} else if n > 0 {
		m.log.Warn("marked jobs interrupted by a restart as failed", "count", n)
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.work(ctx)
	}
	return nil
}

// Stop drains the workers.
func (m *Manager) Stop() {
	if !m.started {
		return
	}
	m.cancel()
	m.wg.Wait()
	m.started = false
}

// Submit queues a job and returns immediately.
func (m *Manager) Submit(ctx context.Context, spec Spec) error {
	job := &store.Job{
		ID:       spec.ID,
		Action:   spec.Action,
		Target:   spec.Target,
		ActorID:  spec.ActorID,
		Actor:    spec.Actor,
		State:    store.JobQueued,
		QueuedAt: time.Now().UTC(),
		DryRun:   spec.Request.DryRun,
	}
	// The argv is recorded before the job runs, so a queue somebody is looking at
	// says what each entry is about to do rather than only what it did.
	if argv, err := m.argv(ctx, spec); err == nil {
		job.Argv = strings.Join(argv, " ")
	}
	if err := m.store.CreateJob(ctx, job); err != nil {
		return err
	}
	select {
	case m.queue <- spec:
		return nil
	default:
		// Refused rather than blocked: a handler that blocks here holds a request
		// open on a queue that is already too long.
		_ = m.finish(context.Background(), &store.Job{
			ID: spec.ID, State: store.JobFailed, FinishedAt: time.Now().UTC(),
			Error: "the job queue is full",
		})
		return rlerr.Preconditionf("the job queue is full").
			WithHint("wait for the running jobs to finish; ratline serialises mutations anyway")
	}
}

func (m *Manager) argv(ctx context.Context, spec Spec) ([]string, error) {
	cat, err := m.client.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	return rl.BuildArgv(cat, spec.Policy, spec.Request)
}

func (m *Manager) work(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case spec := <-m.queue:
			m.run(ctx, spec)
		}
	}
}

func (m *Manager) run(ctx context.Context, spec Spec) {
	started := time.Now().UTC()
	if err := m.store.StartJob(ctx, spec.ID, started); err != nil {
		m.log.Error("could not mark the job started", "job", spec.ID, "err", err)
	}
	buf := &buffer{limit: m.outputLimit, onLine: func(line string) { m.publish(spec.ID, line) }}
	m.mu.Lock()
	m.live[spec.ID] = buf
	m.mu.Unlock()

	job := &store.Job{ID: spec.ID, State: store.JobDone}

	cat, err := m.client.Catalogue(ctx)
	if err == nil {
		var out *rl.Outcome
		out, err = m.client.Stream(ctx, cat, spec.Policy, spec.Request, buf)
		if err == nil {
			job.ExitCode = out.ExitCode
			if cmdErr := out.Err(); cmdErr != nil {
				err = cmdErr
			}
		}
	}
	if err != nil {
		job.State = store.JobFailed
		job.Error = err.Error()
		job.Hint = rlerr.Hint(err)
		if job.ExitCode == 0 {
			job.ExitCode = rlerr.ExitCode(err)
		}
	}
	job.FinishedAt = time.Now().UTC()
	job.Output = buf.String()

	if ferr := m.finish(ctx, job); ferr != nil {
		m.log.Error("could not record the job's outcome", "job", spec.ID, "err", ferr)
	}
	m.log.Info("job finished", "job", spec.ID, "action", spec.Action, "target", spec.Target,
		"state", job.State, "exit", job.ExitCode,
		"ms", job.FinishedAt.Sub(started).Milliseconds())

	// Told, then closed. A browser watching the stream needs to know the job ended
	// even when the last thing it printed was ordinary.
	m.publish(spec.ID, "\x00"+job.State)
	m.closeSubs(spec.ID)
}

func (m *Manager) finish(ctx context.Context, job *store.Job) error {
	if err := m.store.FinishJob(ctx, job); err != nil {
		return err
	}
	if m.retain > 0 {
		if _, err := m.store.PurgeJobs(ctx, m.retain); err != nil {
			m.log.Debug("could not trim the job history", "err", err)
		}
	}
	return nil
}

// Subscribe returns a channel of transcript lines for a running job, and the lines
// already produced. Closed when the job ends.
//
// The backlog matters: without it, opening a deploy that started ten seconds ago
// shows an empty pane until the next line arrives, which reads as a hang.
func (m *Manager) Subscribe(id string) (backlog string, ch <-chan string, live bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	buf, ok := m.live[id]
	if !ok {
		return "", nil, false
	}
	c := make(chan string, 256)
	m.subs[id] = append(m.subs[id], c)
	return buf.String(), c, true
}

// Unsubscribe releases a subscription when the browser goes away.
func (m *Manager) Unsubscribe(id string, ch <-chan string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := m.subs[id]
	for i, c := range subs {
		if (<-chan string)(c) == ch {
			m.subs[id] = append(subs[:i], subs[i+1:]...)
			close(c)
			return
		}
	}
}

func (m *Manager) publish(id, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.subs[id] {
		select {
		case c <- line:
		default:
			// A subscriber that is not reading must not stall the job producing
			// the output. Dropping a line for a stalled browser is the right
			// trade: the transcript is stored in full and reloading shows it.
		}
	}
}

func (m *Manager) closeSubs(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.subs[id] {
		close(c)
	}
	delete(m.subs, id)
	delete(m.live, id)
}
