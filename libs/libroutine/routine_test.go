package libroutine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/js402/CATE/libs/libroutine"
)

func TestCircuitBreaker_ClosedState_AllowsExecution(t *testing.T) {
	rm := libroutine.NewRoutine(3, time.Second)

	if !rm.Allow() {
		t.Errorf("expected Allow to return true in closed state")
	}

	err := rm.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Errorf("expected Execute to succeed, got error: %v", err)
	}
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	rm := libroutine.NewRoutine(1, 500*time.Millisecond)

	err := rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	if err == nil {
		t.Errorf("expected Execute to return an error")
	}

	if rm.Allow() {
		t.Errorf("expected Allow to return false after failure threshold exceeded")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	// Cause the circuit to open
	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	// Wait for reset timeout (use polling instead of sleep)
	deadline := time.Now().Add(202 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rm.Allow() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// First call in half-open should be allowed
	if !rm.Allow() {
		t.Errorf("expected Allow to return true in half-open state")
	}

	// Second call in half-open should be blocked
	if rm.Allow() {
		t.Errorf("expected Allow to return false in half-open state when test call is in progress")
	}
}

func TestCircuitBreaker_RecoversFromHalfOpenOnSuccess(t *testing.T) {
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	// Cause the circuit to open
	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	// Wait for reset timeout
	time.Sleep(250 * time.Millisecond)

	// First call in half-open should be allowed and succeed
	err := rm.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Errorf("expected Execute to succeed in half-open state, got error: %v", err)
	}

	// Ensure further calls are allowed (circuit should be fully closed again)
	if !rm.Allow() {
		t.Errorf("expected Allow to return true after recovering from half-open state")
	}
}

func TestCircuitBreaker_ReopensAfterFailureInHalfOpen(t *testing.T) {
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	// Cause the circuit to open
	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	// Wait for reset timeout
	time.Sleep(250 * time.Millisecond)

	// First call in half-open should be allowed but fail
	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	// Circuit should now be open again, blocking calls
	if rm.Allow() {
		t.Errorf("expected Allow to return false after failure in half-open state")
	}
}

func TestCircuitBreaker_LoopExecutesFunction(t *testing.T) {
	rm := libroutine.NewRoutine(1, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggerChan := make(chan struct{})
	callCount := 0
	fn := func(ctx context.Context) error {
		callCount++
		return nil
	}

	// Start the loop in a separate goroutine.
	go rm.Loop(ctx, 100*time.Millisecond, triggerChan, fn, func(err error) {})

	// Let the loop run for a short while.
	time.Sleep(350 * time.Millisecond)
	cancel()

	// Give a moment for the loop to exit.
	time.Sleep(150 * time.Millisecond)

	if callCount < 2 {
		t.Errorf("expected loop to execute at least 2 calls, got %d", callCount)
	}
}
func TestCircuitBreaker_GetState(t *testing.T) {
	rm := libroutine.NewRoutine(2, time.Second)

	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected initial state to be Closed, got %v", rm.GetState())
	}

	// Force the state to Open and check
	rm.ForceOpen()
	if rm.GetState() != libroutine.Open {
		t.Errorf("expected state to be Open after ForceOpen, got %v", rm.GetState())
	}

	// Force the state to Closed and check
	rm.ForceClose()
	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected state to be Closed after ForceClose, got %v", rm.GetState())
	}
}

func TestCircuitBreaker_GetThreshold(t *testing.T) {
	rm := libroutine.NewRoutine(3, time.Second)

	if rm.GetThreshold() != 3 {
		t.Errorf("expected threshold to be 3, got %d", rm.GetThreshold())
	}
}

func TestCircuitBreaker_GetResetTimeout(t *testing.T) {
	rm := libroutine.NewRoutine(3, 2*time.Second)

	if rm.GetResetTimeout() != 2*time.Second {
		t.Errorf("expected reset timeout to be 2 seconds, got %v", rm.GetResetTimeout())
	}
}
func TestCircuitBreaker_ForceOpen(t *testing.T) {
	rm := libroutine.NewRoutine(2, time.Second)

	rm.ForceOpen()
	if rm.GetState() != libroutine.Open {
		t.Errorf("expected state to be Open after ForceOpen, got %v", rm.GetState())
	}

	if rm.Allow() {
		t.Errorf("expected Allow to return false after ForceOpen")
	}
}

func TestCircuitBreaker_ForceClose(t *testing.T) {
	rm := libroutine.NewRoutine(2, time.Second)

	// Force the circuit to open
	rm.ForceOpen()
	rm.ForceClose()

	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected state to be Closed after ForceClose, got %v", rm.GetState())
	}

	if !rm.Allow() {
		t.Errorf("expected Allow to return true after ForceClose")
	}
}
