package libroutine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// State represents the state of the circuit breaker.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

// Routine is a circuit breaker with support for a half-open state.
type Routine struct {
	mu            sync.Mutex
	failureCount  int
	threshold     int
	state         State
	lastFailureAt time.Time
	resetTimeout  time.Duration
	inTest        bool
}

// NewRoutine creates a new Routine with a given failure threshold and reset timeout.
func NewRoutine(threshold int, resetTimeout time.Duration) *Routine {
	log.Printf("Creating new routine with threshold: %d, reset timeout: %s", threshold, resetTimeout)
	return &Routine{
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        Closed,
	}
}

// Allow determines whether an operation is allowed to run based on the current state.
func (rm *Routine) Allow() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		return true
	case Open:
		if time.Since(rm.lastFailureAt) > rm.resetTimeout {
			//	log.Println("Reset timeout elapsed, transitioning to HalfOpen state")
			rm.state = HalfOpen
			rm.inTest = false
		} else {
			//	log.Println("Circuit breaker is open, request denied")
			return false
		}
	case HalfOpen:
		if rm.inTest {
			//	log.Println("HalfOpen state: Test request already in progress, request denied")
			return false
		}
	}

	if rm.state == HalfOpen && !rm.inTest {
		//	log.Println("HalfOpen state: Allowing test request")
		rm.inTest = true
	}
	return true
}

// MarkSuccess resets the circuit breaker after a successful call.
func (rm *Routine) MarkSuccess() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		rm.failureCount = 0
	case HalfOpen:
		//	log.Println("HalfOpen state: Test request succeeded, transitioning to Closed")
		rm.state = Closed
		rm.failureCount = 0
		rm.inTest = false
	}
}

// MarkFailure increments the failure counter and changes the state accordingly.
func (rm *Routine) MarkFailure() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.state {
	case Closed:
		rm.failureCount++
		log.Printf("Failure recorded, count: %d", rm.failureCount)
		if rm.failureCount >= rm.threshold {
			log.Println("Threshold reached, transitioning to Open state")
			rm.state = Open
			rm.lastFailureAt = time.Now().UTC()
		}
	case HalfOpen:
		log.Println("HalfOpen state: Test request failed, reverting to Open state")
		rm.state = Open
		rm.lastFailureAt = time.Now().UTC()
		rm.inTest = false
	}
}

// Execute runs the provided function if allowed by the circuit breaker.
func (rm *Routine) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	if !rm.Allow() {
		// log.Println("Execution denied: Circuit breaker is open")
		return fmt.Errorf("circuit breaker is open")
	}

	err := fn(ctx)
	if err != nil {
		// log.Println("Execution failed, marking failure")
		rm.MarkFailure()
	} else {
		// log.Println("Execution succeeded, marking success")
		rm.MarkSuccess()
	}
	return err
}

// ExecuteWithRetry attempts execution multiple times with a delay.
func (rm *Routine) ExecuteWithRetry(ctx context.Context, interval time.Duration, iterations int, fn func(ctx context.Context) error) error {
	var err error
	for i := range iterations {
		if ctx.Err() != nil {
			log.Println("Context cancelled, aborting retries")
			return context.Cause(ctx)
		}
		log.Printf("Retry attempt %d", i+1)
		if err = rm.Execute(ctx, fn); err == nil {
			return nil
		}
		time.Sleep(interval)
	}
	return err
}

// Loop repeatedly executes the provided function using the circuit breaker logic.
func (rm *Routine) Loop(ctx context.Context, interval time.Duration, triggerChan <-chan struct{}, fn func(ctx context.Context) error, errHandling func(err error)) {
	for {
		if err := rm.Execute(ctx, fn); err != nil {
			errHandling(err)
		}
		select {
		case <-ctx.Done():
			log.Println("Loop exiting due to context cancellation")
			return
		case <-triggerChan:
		//	log.Println("Trigger received, executing immediately")
		case <-time.After(interval):
			// log.Println("Interval elapsed, executing next cycle")
		}
	}
}

// ForceOpen sets the circuit breaker to the Open state.
func (rm *Routine) ForceOpen() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	log.Println("Forcing circuit breaker to Open state")
	rm.state = Open
	rm.lastFailureAt = time.Now()
	rm.failureCount = rm.threshold
	rm.inTest = false
}

// ForceClose resets the circuit breaker to the Closed state.
// This method is useful for testing purposes
// or to manually alter the circuit breaker state.
func (rm *Routine) ForceClose() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	log.Println("Forcing circuit breaker to Closed state")
	rm.state = Closed
	rm.failureCount = 0
	rm.inTest = false
}

// GetState returns the current state of the circuit breaker.
func (rm *Routine) GetState() State {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.state
}

// GetThreshold returns the failure threshold of the circuit breaker.
func (rm *Routine) GetThreshold() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.threshold
}

// GetResetTimeout returns the reset timeout duration of the circuit breaker.
func (rm *Routine) GetResetTimeout() time.Duration {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.resetTimeout
}

// ResetRoutine resets the routine for a given key within the pool.
// This method can be used to manually alter the circuit breaker state.
// params:
// - key string, key for the routine to be reset
func (p *Pool) ResetRoutine(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if manager, exists := p.managers[key]; exists {
		manager.ForceClose()
	}
}
