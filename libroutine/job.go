package libroutine

import (
	"context"
	"fmt"
	"time"
)

type Condition func(ctx context.Context) (bool, error)

type Operation func(ctx context.Context) error

type Job struct {
	Name      string
	Condition Condition
	Operation Operation
	Next      *Job
}

type RunResult struct {
	Name     string
	Skipped  bool
	Err      error
	Duration time.Duration
	Next     *RunResult
}

func (r *RunResult) Failed() bool {
	return r.firstErr() != nil
}

func (r *RunResult) firstErr() error {
	for n := r; n != nil; n = n.Next {
		if n.Err != nil {
			return n.Err
		}
	}
	return nil
}

func (j *Job) run(ctx context.Context) *RunResult {
	start := time.Now()
	res := &RunResult{Name: j.Name}
	defer func() { res.Duration = time.Since(start) }()

	if j.Condition != nil {
		ok, err := j.Condition(ctx)
		if err != nil {
			res.Err = fmt.Errorf("libroutine: condition for %q: %w", j.Name, err)
			return res
		}
		if !ok {
			res.Skipped = true
			return res
		}
	}

	if j.Operation != nil {
		if err := j.Operation(ctx); err != nil {
			res.Err = fmt.Errorf("libroutine: operation for %q: %w", j.Name, err)
			return res
		}
	}

	if j.Next != nil {
		select {
		case <-ctx.Done():
			return res
		default:
			res.Next = j.Next.run(ctx)
		}
	}
	return res
}
