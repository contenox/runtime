package libroutine

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "Closed"
	case Open:
		return "Open"
	case HalfOpen:
		return "HalfOpen"
	default:
		return "Unknown"
	}
}

var ErrCircuitOpen = errors.New("circuit breaker is open")

type Routine struct {
	mu            sync.Mutex
	state         State
	failureCount  int
	threshold     int
	resetTimeout  time.Duration
	lastFailureAt time.Time
	inTest        bool
}

func NewRoutine(threshold int, resetTimeout time.Duration) *Routine {
	return &Routine{
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        Closed,
	}
}

func (rm *Routine) Allow() bool {
	return rm.AllowAt(time.Now())
}

func (rm *Routine) AllowAt(now time.Time) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		return true
	case Open:
		if now.Sub(rm.lastFailureAt) > rm.resetTimeout {
			rm.state = HalfOpen
			rm.inTest = false
		} else {
			return false
		}
	case HalfOpen:
		if rm.inTest {
			return false
		}
	}

	if rm.state == HalfOpen && !rm.inTest {
		rm.inTest = true
	}
	return true
}

func (rm *Routine) MarkSuccess() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		rm.failureCount = 0
	case HalfOpen:
		rm.state = Closed
		rm.failureCount = 0
		rm.inTest = false
	}
}

func (rm *Routine) MarkFailure() {
	rm.MarkFailureAt(time.Now())
}

func (rm *Routine) MarkFailureAt(now time.Time) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		rm.failureCount++
		if rm.failureCount >= rm.threshold {
			rm.state = Open
			rm.lastFailureAt = now.UTC()
		}
	case HalfOpen:
		rm.state = Open
		rm.lastFailureAt = now.UTC()
		rm.inTest = false
	}
}

func (rm *Routine) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	if !rm.Allow() {
		return ErrCircuitOpen
	}

	err := fn(ctx)
	if err != nil {
		rm.MarkFailure()
	} else {
		rm.MarkSuccess()
	}
	return err
}

func (rm *Routine) ExecuteWithRetry(ctx context.Context, interval time.Duration, iterations int, fn func(ctx context.Context) error) error {
	var err error
	for range iterations {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err = rm.Execute(ctx, fn); err == nil {
			return nil
		}
		time.Sleep(interval)
	}
	return err
}

func (rm *Routine) Loop(ctx context.Context, interval time.Duration, triggerChan <-chan struct{}, fn func(ctx context.Context) error, errHandling func(err error)) {
	var lastErr error
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := rm.Execute(ctx, fn)
		lastErr = err
		if err != nil {
			errHandling(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-triggerChan:
			if lastErr != nil {
				time.Sleep(interval)
			}
		case <-ticker.C:
		}
	}
}

func (rm *Routine) ForceOpen() {
	rm.ForceOpenAt(time.Now())
}

func (rm *Routine) ForceOpenAt(now time.Time) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	log.Println("Forcing circuit breaker to Open state")
	rm.state = Open
	rm.lastFailureAt = now.UTC()
	rm.failureCount = rm.threshold
	rm.inTest = false
}

func (rm *Routine) ForceClose() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	log.Println("Forcing circuit breaker to Closed state")
	rm.state = Closed
	rm.failureCount = 0
	rm.inTest = false
}

func (rm *Routine) WouldAllowAt(now time.Time) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		return true
	case Open:
		return now.Sub(rm.lastFailureAt) > rm.resetTimeout
	case HalfOpen:
		return !rm.inTest
	}
	return false
}

func (rm *Routine) GetState() State {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.state
}

func (rm *Routine) GetThreshold() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.threshold
}

func (rm *Routine) GetResetTimeout() time.Duration {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.resetTimeout
}

func (p *group) ResetRoutine(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if manager, exists := p.managers[key]; exists {
		manager.ForceClose()
	}
}
