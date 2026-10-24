package libroutine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
)

var ErrAlreadyRunning = errors.New("libroutine: job is already running")

type RunnerOption func(*Runner)

func WithResultHook(fn func(*RunResult)) RunnerOption {
	return func(r *Runner) { r.hook = fn }
}

func WithTracker(tracker libtracker.ActivityTracker) RunnerOption {
	return func(r *Runner) { r.tracker = tracker }
}

type Runner struct {
	job     *Job
	routine *Routine

	mu      sync.Mutex
	running bool

	hook    func(*RunResult)
	tracker libtracker.ActivityTracker
}

func NewRunner(job *Job, threshold int, resetTimeout time.Duration, opts ...RunnerOption) *Runner {
	r := &Runner{
		job:     job,
		routine: NewRoutine(threshold, resetTimeout),
		tracker: libtracker.NoopTracker{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Runner) Run(ctx context.Context) (*RunResult, error) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	var res *RunResult
	execErr := r.routine.Execute(ctx, func(ctx context.Context) error {
		res = r.job.run(ctx)
		return res.firstErr()
	})
	if res == nil {
		return nil, execErr
	}

	reportErr, reportChange, end := r.tracker.Start(ctx, "run", "job", "name", r.job.Name)
	defer end()
	if err := res.firstErr(); err != nil {
		reportErr(err)
	} else {
		reportChange(r.job.Name, res)
	}

	if r.hook != nil {
		r.hook(res)
	}
	return res, nil
}

func (r *Runner) Trigger(ctx context.Context) {
	go func() { _, _ = r.Run(ctx) }()
}

func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
