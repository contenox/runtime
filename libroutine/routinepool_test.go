package libroutine_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/libroutine"
)

func TestUnit_GroupAffinitySingleton(t *testing.T) {
	defer quiet()
	t.Run("should return singleton instance", func(t *testing.T) {
		group1 := libroutine.GetGroup()
		group2 := libroutine.GetGroup()
		if group1 != group2 {
			t.Error("Expected group to be singleton, got different instances")
		}
	})
}

func TestUnit_GroupAffinityStartLoop(t *testing.T) {
	group := libroutine.GetGroup()
	ctx := t.Context()

	t.Run("should create new manager and start loop", func(t *testing.T) {
		key := "test-service"
		var callCount int
		var mu sync.Mutex

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    2,
			ResetTimeout: 100 * time.Millisecond,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				mu.Lock()
				callCount++
				mu.Unlock()
				return nil
			},
		})

		time.Sleep(25 * time.Millisecond)

		// Windows timer granularity can be coarser than the loop's 10ms
		// interval, so wait for the first tick instead of assuming 25ms is
		// enough on a loaded machine.
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			n := callCount
			mu.Unlock()
			if n >= 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("Expected at least 1 call, got %d", n)
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		if !group.IsLoopActive(key) {
			t.Error("Loop should be tracked as active")
		}
	})

	t.Run("should prevent duplicate loops for same key", func(t *testing.T) {
		key := "duplicate-test"
		var callCount int
		var mu sync.Mutex

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    1,
			ResetTimeout: time.Second,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				mu.Lock()
				callCount++
				mu.Unlock()
				return nil
			},
		})

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    1,
			ResetTimeout: time.Second,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				mu.Lock()
				callCount++
				mu.Unlock()
				return nil
			},
		})

		time.Sleep(25 * time.Millisecond)

		mu.Lock()
		if callCount < 1 {
			t.Errorf("Expected at least 1 call, got %d", callCount)
		}
		if callCount > 3 {
			t.Errorf("Expected approximately 2-3 calls, got %d (too many, duplicate loop might be running)", callCount)
		}
		mu.Unlock()
	})

	t.Run("should clean up after context cancellation", func(t *testing.T) {
		key := "cleanup-test"
		localCtx, localCancel := context.WithCancel(ctx)

		group.StartLoop(localCtx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    1,
			ResetTimeout: time.Second,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				return nil
			},
		})

		time.Sleep(10 * time.Millisecond)
		localCancel()

		time.Sleep(50 * time.Millisecond)

		if group.IsLoopActive(key) {
			t.Error("Loop should be removed from active tracking")
		}
	})

	t.Run("should handle concurrent StartLoop calls", func(t *testing.T) {
		key := "concurrency-test"
		var wg sync.WaitGroup
		var callCount int
		var mu sync.Mutex
		loopCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				group.StartLoop(loopCtx, &libroutine.LoopConfig{
					Key:          key,
					Threshold:    1,
					ResetTimeout: time.Second,
					Interval:     10 * time.Millisecond,
					Operation: func(ctx context.Context) error {
						mu.Lock()
						callCount++
						mu.Unlock()
						return nil
					},
				})
			}()
		}

		wg.Wait()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			n := callCount
			mu.Unlock()
			if n > 0 {
				if n > 6 {
					t.Errorf("Expected approximately 5-6 calls, got %d (too many, concurrency issue)", n)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Error("Expected at least 1 call, got 0")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

func TestUnit_GroupAffinityCircuitBreaking(t *testing.T) {
	defer quiet()
	group := libroutine.GetGroup()
	ctx := context.Background()

	t.Run("should enforce circuit breaker parameters", func(t *testing.T) {
		key := "circuit-params-test"
		failureThreshold := 3
		resetTimeout := 100 * time.Millisecond

		var failures int
		var mu sync.Mutex

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    failureThreshold,
			ResetTimeout: resetTimeout,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				mu.Lock()
				failures++
				mu.Unlock()
				return fmt.Errorf("simulated failure")
			},
		})

		time.Sleep(50 * time.Millisecond)

		manager := group.GetManager(key)
		if manager == nil {
			t.Fatal("Manager not created")
		}

		if manager.GetState() != libroutine.Open {
			t.Errorf("Expected circuit to be open after failures, got state %v", manager.GetState())
		}

		time.Sleep(resetTimeout)
		err := manager.Execute(ctx, func(ctx context.Context) error {
			return fmt.Errorf("simulated failure")
		})
		if err == nil {
			t.Fatal("expected probe execution to fail")
		}
		if manager.GetState() != libroutine.Open {
			t.Errorf("Expected circuit open after failed half-open probe, got %v", manager.GetState())
		}
	})
}

func TestUnit_GroupAffinityParameterPersistence(t *testing.T) {
	defer quiet()
	group := libroutine.GetGroup()
	ctx := context.Background()

	t.Run("should persist initial parameters", func(t *testing.T) {
		key := "param-persistence-test"
		initialThreshold := 2
		initialTimeout := 100 * time.Millisecond

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    initialThreshold,
			ResetTimeout: initialTimeout,
			Interval:     10 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				return nil
			},
		})

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    5,
			ResetTimeout: 200 * time.Millisecond,
			Interval:     20 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				return nil
			},
		})

		manager := group.GetManager(key)
		if manager == nil {
			t.Fatal("Manager not created")
		}

		if manager.GetThreshold() != initialThreshold {
			t.Errorf("Expected threshold %d, got %d", initialThreshold, manager.GetThreshold())
		}
		if manager.GetResetTimeout() != initialTimeout {
			t.Errorf("Expected timeout %v, got %v", initialTimeout, manager.GetResetTimeout())
		}
	})
}

func TestUnit_GroupAffinityResetRoutine(t *testing.T) {
	defer quiet()
	group := libroutine.GetGroup()
	ctx := context.Background()

	t.Run("should reset routine state", func(t *testing.T) {
		key := "reset-routine-test"
		var runCount int
		var mu sync.Mutex
		failureOccurred := make(chan bool, 1)

		group.StartLoop(ctx, &libroutine.LoopConfig{
			Key:          key,
			Threshold:    1,
			ResetTimeout: 50 * time.Millisecond,
			Interval:     5 * time.Millisecond,
			Operation: func(ctx context.Context) error {
				mu.Lock()
				runCount++
				currentCount := runCount
				mu.Unlock()

				if currentCount == 1 {
					select {
					case failureOccurred <- true:
					default:
					}
					return errors.New("fail once")
				}
				return nil
			},
		})

		<-failureOccurred

		manager := group.GetManager(key)
		if manager == nil {
			t.Fatalf("Manager for key %s not found", key)
		}

		if manager.GetState() != libroutine.Open {
			t.Fatalf("Expected circuit to be open after failure, got %v", manager.GetState())
		}

		for range 50000 {
			if manager.GetState() == libroutine.Closed {
				return
			}
			group.ForceUpdate(key)
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("expected Closed, got %v", manager.GetState())
	})
}

func TestUnit_Pool(t *testing.T) {
	defer quiet()
	pool := libroutine.NewPool(2, 5*time.Minute)
	now := time.Now()

	if !pool.AllowAt("key-1", now) {
		t.Fatal("expected key-1 to be allowed initially")
	}

	r := pool.Get("key-1")
	if r == nil {
		t.Fatal("expected Get to return Routine")
	}

	r.MarkFailureAt(now)
	r.MarkFailureAt(now)

	if pool.AllowAt("key-1", now.Add(1*time.Minute)) {
		t.Fatal("expected key-1 to be disallowed before reset timeout")
	}
	if !pool.AllowAt("key-1", now.Add(6*time.Minute)) {
		t.Fatal("expected key-1 to be allowed after reset timeout")
	}

	if !pool.AllowAt("key-2", now) {
		t.Fatal("expected key-2 to be unaffected by key-1 failures")
	}

	pool.MarkSuccess("key-1")
	if !pool.AllowAt("key-1", now) {
		t.Fatal("expected key-1 to be allowed after MarkSuccess")
	}
}
