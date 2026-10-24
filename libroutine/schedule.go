package libroutine

import (
	"context"
	"time"
)

type Schedule interface {
	Next(t time.Time) time.Time
}

type everySchedule struct{ d time.Duration }

func (e everySchedule) Next(t time.Time) time.Time { return t.Add(e.d) }

func Every(d time.Duration) Schedule { return everySchedule{d: d} }

func (r *Runner) StartSchedule(ctx context.Context, sched Schedule) {
	go r.runSchedule(ctx, sched)
}

func (r *Runner) runSchedule(ctx context.Context, sched Schedule) {
	next := sched.Next(time.Now())
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-timer.C:
			r.Trigger(ctx)
			next = sched.Next(t)
			d := time.Until(next)
			if d < 0 {
				d = 0
			}
			timer.Reset(d)
		}
	}
}
