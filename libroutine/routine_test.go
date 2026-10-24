package libroutine_test

import (
	"context"
	"errors"
	"log"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/libroutine"
)

func quiet() func() {
	null, _ := os.Open(os.DevNull)
	sout := os.Stdout
	serr := os.Stderr
	os.Stdout = null
	os.Stderr = null
	log.SetOutput(null)
	return func() {
		defer null.Close()
		os.Stdout = sout
		os.Stderr = serr
		log.SetOutput(os.Stderr)
	}
}

func TestUnit_CircuitBreaker_ClosedState_AllowsExecution(t *testing.T) {
	defer quiet()()
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

func TestUnit_CircuitBreaker_OpensAfterFailures(t *testing.T) {
	defer quiet()()
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

func TestUnit_CircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	deadline := time.Now().Add(202 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rm.Allow() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !rm.Allow() {
		t.Errorf("expected Allow to return true in half-open state")
	}

	if rm.Allow() {
		t.Errorf("expected Allow to return false in half-open state when test call is in progress")
	}
}

func TestUnit_CircuitBreaker_RecoversFromHalfOpenOnSuccess(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	time.Sleep(250 * time.Millisecond)

	err := rm.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Errorf("expected Execute to succeed in half-open state, got error: %v", err)
	}

	if !rm.Allow() {
		t.Errorf("expected Allow to return true after recovering from half-open state")
	}
}

func TestUnit_CircuitBreaker_ReopensAfterFailureInHalfOpen(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, 200*time.Millisecond)

	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	time.Sleep(250 * time.Millisecond)

	_ = rm.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("test error")
	})

	if rm.Allow() {
		t.Errorf("expected Allow to return false after failure in half-open state")
	}
}

func TestUnit_CircuitBreaker_LoopExecutesFunction(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggerChan := make(chan struct{})
	var callCount int32
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	go rm.Loop(ctx, 100*time.Millisecond, triggerChan, fn, func(err error) {})

	time.Sleep(350 * time.Millisecond)
	cancel()

	time.Sleep(150 * time.Millisecond)

	if atomic.LoadInt32(&callCount) < 2 {
		t.Errorf("expected loop to execute at least 2 calls, got %d", callCount)
	}
}
func TestUnit_CircuitBreaker_GetState(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(2, time.Second)

	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected initial state to be Closed, got %v", rm.GetState())
	}

	rm.ForceOpen()
	if rm.GetState() != libroutine.Open {
		t.Errorf("expected state to be Open after ForceOpen, got %v", rm.GetState())
	}

	rm.ForceClose()
	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected state to be Closed after ForceClose, got %v", rm.GetState())
	}
}

func TestUnit_CircuitBreaker_GetThreshold(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(3, time.Second)

	if rm.GetThreshold() != 3 {
		t.Errorf("expected threshold to be 3, got %d", rm.GetThreshold())
	}
}

func TestUnit_CircuitBreaker_GetResetTimeout(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(3, 2*time.Second)

	if rm.GetResetTimeout() != 2*time.Second {
		t.Errorf("expected reset timeout to be 2 seconds, got %v", rm.GetResetTimeout())
	}
}
func TestUnit_CircuitBreaker_ForceOpen(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(2, time.Second)

	rm.ForceOpen()
	if rm.GetState() != libroutine.Open {
		t.Errorf("expected state to be Open after ForceOpen, got %v", rm.GetState())
	}

	if rm.Allow() {
		t.Errorf("expected Allow to return false after ForceOpen")
	}
}

func TestUnit_CircuitBreaker_ForceClose(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(2, time.Second)

	rm.ForceOpen()
	rm.ForceClose()

	if rm.GetState() != libroutine.Closed {
		t.Errorf("expected state to be Closed after ForceClose, got %v", rm.GetState())
	}

	if !rm.Allow() {
		t.Errorf("expected Allow to return true after ForceClose")
	}
}

func TestUnit_CircuitBreaker_ExecuteReturnsErrCircuitOpen(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, time.Minute)

	rm.ForceOpen()

	err := rm.Execute(context.Background(), func(ctx context.Context) error {
		t.Error("Function should not have been executed when circuit is open")
		return nil
	})

	if !errors.Is(err, libroutine.ErrCircuitOpen) {
		t.Errorf("Expected error to be ErrCircuitOpen, got %v", err)
	}
}

func TestUnit_CircuitBreaker_ExecuteWithRetry(t *testing.T) {
	defer quiet()()
	t.Run("SuccessFirstTry", func(t *testing.T) {
		rm := libroutine.NewRoutine(1, time.Minute)
		var callCount int32
		fn := func(ctx context.Context) error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}
		err := rm.ExecuteWithRetry(context.Background(), 10*time.Millisecond, 3, fn)
		if err != nil {
			t.Errorf("Expected success, got error: %v", err)
		}
		if atomic.LoadInt32(&callCount) != 1 {
			t.Errorf("Expected function to be called 1 time, got %d", atomic.LoadInt32(&callCount))
		}
	})

	t.Run("SuccessAfterRetry", func(t *testing.T) {
		rm := libroutine.NewRoutine(5, time.Minute)
		var callCount int32
		testErr := errors.New("retry error")
		fn := func(ctx context.Context) error {
			count := atomic.AddInt32(&callCount, 1)
			if count < 3 {
				return testErr
			}
			return nil
		}
		err := rm.ExecuteWithRetry(context.Background(), 10*time.Millisecond, 5, fn)
		if err != nil {
			t.Errorf("Expected success after retries, got error: %v", err)
		}
		if atomic.LoadInt32(&callCount) != 3 {
			t.Errorf("Expected function to be called 3 times, got %d", atomic.LoadInt32(&callCount))
		}
	})

	t.Run("FailureAllRetries", func(t *testing.T) {
		rm := libroutine.NewRoutine(5, time.Minute)
		var callCount int32
		testErr := errors.New("persistent error")
		fn := func(ctx context.Context) error {
			atomic.AddInt32(&callCount, 1)
			return testErr
		}
		err := rm.ExecuteWithRetry(context.Background(), 10*time.Millisecond, 3, fn)
		if !errors.Is(err, testErr) {
			t.Errorf("Expected persistent error %v, got %v", testErr, err)
		}
		if atomic.LoadInt32(&callCount) != 3 {
			t.Errorf("Expected function to be called 3 times, got %d", atomic.LoadInt32(&callCount))
		}
	})

	t.Run("FailureCircuitOpen", func(t *testing.T) {
		rm := libroutine.NewRoutine(1, time.Minute)
		var callCount int32
		fn := func(ctx context.Context) error {
			atomic.AddInt32(&callCount, 1)
			return errors.New("failure")
		}
		_ = rm.Execute(context.Background(), fn)
		if rm.GetState() != libroutine.Open {
			t.Fatalf("Circuit should be open")
		}

		atomic.StoreInt32(&callCount, 0)
		err := rm.ExecuteWithRetry(context.Background(), 10*time.Millisecond, 3, fn)

		if !errors.Is(err, libroutine.ErrCircuitOpen) {
			t.Errorf("Expected ErrCircuitOpen when circuit is already open, got %v", err)
		}
		if atomic.LoadInt32(&callCount) != 0 {
			t.Errorf("Expected function not to be called when circuit is open, called %d times", atomic.LoadInt32(&callCount))
		}
	})

	t.Run("ContextCancelledDuringSleep", func(t *testing.T) {
		rm := libroutine.NewRoutine(5, time.Minute)
		var callCount int32
		testErr := errors.New("fail first")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		fn := func(innerCtx context.Context) error {
			count := atomic.AddInt32(&callCount, 1)
			if count == 1 {
				go func() {
					time.Sleep(5 * time.Millisecond)
					cancel()
				}()
				return testErr
			}
			t.Errorf("Function should not be called more than once")
			return nil
		}

		err := rm.ExecuteWithRetry(ctx, 50*time.Millisecond, 3, fn)

		if !errors.Is(err, context.Canceled) {
			t.Errorf("Expected context.Canceled error, got %v", err)
		}
		if atomic.LoadInt32(&callCount) != 1 {
			t.Errorf("Expected function to be called 1 time, got %d", atomic.LoadInt32(&callCount))
		}
	})

}

func TestRoutine_Loop_Trigger(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(1, time.Minute)
	ctx := t.Context()

	triggerChan := make(chan struct{}, 1)
	executedChan := make(chan bool, 2)

	fn := func(ctx context.Context) error {
		select {
		case executedChan <- true:
		default:
		}
		return nil
	}

	go rm.Loop(ctx, 1*time.Minute, triggerChan, fn, func(err error) {})

	select {
	case <-executedChan:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Initial execution did not occur as expected")
	}

	triggerChan <- struct{}{}

	select {
	case <-executedChan:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Function did not execute after trigger within timeout")
	}
}

func TestUnit_CircuitBreaker_Loop_ErrHandling(t *testing.T) {
	defer quiet()()
	resetTimeout := 200 * time.Millisecond
	interval := 20 * time.Millisecond
	rm := libroutine.NewRoutine(1, resetTimeout)
	ctx := t.Context()

	triggerChan := make(chan struct{}, 1)
	errChan := make(chan error, 2)
	testErr := errors.New("loop function error")

	fn := func(ctx context.Context) error {
		return testErr
	}

	go rm.Loop(ctx, interval, triggerChan, fn, func(err error) {
		select {
		case errChan <- err:
		default:
		}
	})

	select {
	case <-errChan:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for initial error")
	}

	triggerChan <- struct{}{}
	select {
	case err := <-errChan:
		if !errors.Is(err, libroutine.ErrCircuitOpen) {
			t.Errorf("Expected ErrCircuitOpen, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for open state error")
	}
}

func TestUnit_CircuitBreaker_ExplicitTimestamps(t *testing.T) {
	defer quiet()()
	rm := libroutine.NewRoutine(2, 5*time.Minute)
	now := time.Now()

	if !rm.AllowAt(now) {
		t.Fatal("expected AllowAt to be true initially")
	}

	rm.MarkFailureAt(now)
	if !rm.AllowAt(now) {
		t.Fatal("expected AllowAt to be true before threshold")
	}

	rm.MarkFailureAt(now)
	if rm.GetState() != libroutine.Open {
		t.Fatal("expected Open state after reaching threshold")
	}
	if rm.AllowAt(now.Add(1 * time.Minute)) {
		t.Fatal("expected AllowAt to be false before reset timeout")
	}
	if !rm.AllowAt(now.Add(6 * time.Minute)) {
		t.Fatal("expected AllowAt to be true after reset timeout")
	}

	rm.ForceOpenAt(now)
	if rm.GetState() != libroutine.Open {
		t.Fatal("expected Open state after ForceOpenAt")
	}
	if rm.AllowAt(now.Add(2 * time.Minute)) {
		t.Fatal("expected AllowAt to be false before reset timeout after ForceOpenAt")
	}
	if !rm.AllowAt(now.Add(6 * time.Minute)) {
		t.Fatal("expected AllowAt to be true after reset timeout after ForceOpenAt")
	}
}
