// Package tasks tracks the long-running work an MCP call cannot afford to block on.
//
// WHY THIS EXISTS. A tool call is a request/response pair with a deadline somewhere
// upstream -- an HTTP client, an agent's turn, a proxy's idle timer. A file transfer is
// not. Pushing a 200 MB image over a metered link takes minutes, and a tool that holds
// the call open for the whole time gets killed by something that has no idea it was
// making progress.
//
// So a transfer starts here and answers with an id. A caller that expects a small file
// can ask to wait a few seconds and usually gets the answer inline; a caller moving
// something large gets a handle to poll, and the transfer keeps running either way.
package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// State is the lifecycle of a task, and these strings are what a caller sees.
type State string

const (
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
	Stopped State = "cancelled"
)

// Task is one unit of background work, readable at any time from any goroutine.
type Task struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Device   string `json:"device"`
	State    State  `json:"state"`
	Total    int64  `json:"total_bytes,omitempty"`
	Moved    int64  `json:"moved_bytes"`
	Error    string `json:"error,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Started  string `json:"started_at"`
	Finished string `json:"finished_at,omitempty"`

	mu     sync.Mutex
	cancel chan struct{}
	once   sync.Once
	done   chan struct{}
}

func (t *Task) progress(n int64) {
	t.mu.Lock()
	t.Moved += n
	t.mu.Unlock()
}

func (t *Task) setTotal(n int64) {
	t.mu.Lock()
	t.Total = n
	t.mu.Unlock()
}

func (t *Task) snapshot() Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Copy field by field rather than *t: the struct owns a mutex, and copying one
	// wholesale copies the lock state too, which vet rightly refuses.
	return Task{
		ID:       t.ID,
		Kind:     t.Kind,
		Device:   t.Device,
		State:    t.State,
		Total:    t.Total,
		Moved:    t.Moved,
		Error:    t.Error,
		Detail:   t.Detail,
		Started:  t.Started,
		Finished: t.Finished,
	}
}

func (t *Task) finish(state State, detail string, err error) {
	t.mu.Lock()
	if t.State != Running {
		t.mu.Unlock()
		return
	}
	t.State = state
	t.Detail = detail
	if err != nil {
		t.Error = err.Error()
	}
	t.Finished = time.Now().Format(time.RFC3339)
	t.mu.Unlock()
	close(t.done)
}

// Handle is what a body receives: progress to report, and a way to see a cancel.
type Handle struct {
	Task      *Task
	Cancelled <-chan struct{}
}

// Report adds to the moved-byte count.
func (h *Handle) Report(n int64) { h.Task.progress(n) }

// SetTotal records the expected size, when it is known.
func (h *Handle) SetTotal(n int64) { h.Task.setTotal(n) }

// Err returns a non-nil error once the task has been cancelled, so a copy loop can
// stop at a message boundary instead of finishing work nobody wants.
func (h *Handle) Err() error {
	select {
	case <-h.Cancelled:
		return errCancelled
	default:
		return nil
	}
}

var errCancelled = &cancelError{}

type cancelError struct{}

func (*cancelError) Error() string { return "cancelled" }

// Registry holds every task this process has started.
//
// Finished tasks are kept rather than deleted: a caller that polled once and comes back
// later deserves the answer, not "unknown task". The cost is a few hundred bytes each,
// and a server that forgets is worse than one that remembers.
type Registry struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	order []string
}

func NewRegistry() *Registry {
	return &Registry{tasks: make(map[string]*Task)}
}

// Start registers a task and begins running body in the background.
func (r *Registry) Start(kind, device string, body func(h *Handle) error) Task {
	t := &Task{
		ID:      newID(),
		Kind:    kind,
		Device:  device,
		State:   Running,
		Started: time.Now().Format(time.RFC3339),
		cancel:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	h := &Handle{Task: t, Cancelled: t.cancel}

	r.mu.Lock()
	r.tasks[t.ID] = t
	r.order = append(r.order, t.ID)
	r.mu.Unlock()

	go func() {
		if err := body(h); err != nil {
			t.finish(Failed, "", err)
			return
		}
		t.finish(Done, "", nil)
	}()

	return t.snapshot()
}

// Get returns a task snapshot, or false when the id is unknown.
func (r *Registry) Get(id string) (Task, bool) {
	r.mu.RLock()
	t, ok := r.tasks[id]
	r.mu.RUnlock()
	if !ok {
		return Task{}, false
	}
	return t.snapshot(), true
}

// List returns every task, oldest first.
func (r *Registry) List() []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Task, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.tasks[id].snapshot())
	}
	return out
}

// Cancel stops a running task. It reports false when the id is unknown or the work has
// already finished, because cancelling something that is over is not worth an error.
func (r *Registry) Cancel(id string) bool {
	r.mu.RLock()
	t, ok := r.tasks[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	t.mu.Lock()
	running := t.State == Running
	t.mu.Unlock()
	if !running {
		return false
	}
	t.once.Do(func() { close(t.cancel) })
	return true
}

// Wait blocks until the task finishes or the allowance passes, and reports which.
// This is what lets a small transfer answer inline without a second call.
func (r *Registry) Wait(id string, d time.Duration) (Task, bool) {
	r.mu.RLock()
	t, ok := r.tasks[id]
	r.mu.RUnlock()
	if !ok {
		return Task{}, false
	}
	select {
	case <-t.done:
		return t.snapshot(), true
	case <-time.After(d):
		return t.snapshot(), false
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "task_" + hex.EncodeToString(b)
}

// ProgressWriter adds to a task's moved count as bytes pass through it.
type ProgressWriter struct {
	H *Handle
}

func (p ProgressWriter) Write(b []byte) (int, error) {
	if err := p.H.Err(); err != nil {
		return 0, err
	}
	p.H.Report(int64(len(b)))
	return len(b), nil
}
