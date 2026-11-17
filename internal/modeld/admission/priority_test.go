package admission_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Admission_AutocompletePriorityPreservesChatAndEmbeddingFairness(t *testing.T) {
	for _, burst := range []int{1, 2} {
		t.Run(string(rune('0'+burst)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "scheduler.db"), runtimetypes.SchemaSQLite)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			bus := libbus.NewSQLite(db.WithoutTransaction())
			t.Cleanup(func() { require.NoError(t, bus.Close()) })
			scheduler, err := admission.NewEventScheduler(ctx, db, bus, admission.Config{QueueCapacity: 8, GlobalLimit: 1, ForegroundBurst: burst}, func(err error) { t.Error(err) })
			require.NoError(t, err)
			require.NoError(t, scheduler.Register(ctx, admission.Worker{ID: "worker", OwnerEpoch: "owner", Ready: true}))
			request := func(priority admission.Priority) admission.Request {
				return admission.Request{WorkerID: "worker", OwnerEpoch: "owner", Priority: priority}
			}
			hold, err := scheduler.Acquire(ctx, request(admission.Background))
			require.NoError(t, err)
			type granted struct {
				priority admission.Priority
				permit   admission.Permit
				err      error
			}
			results := make(chan granted, 5)
			for _, priority := range []admission.Priority{admission.Foreground, admission.Background, admission.Autocomplete, admission.Autocomplete, admission.Autocomplete} {
				go func() {
					permit, err := scheduler.Acquire(ctx, request(priority))
					results <- granted{priority, permit, err}
				}()
			}
			require.Eventually(t, func() bool {
				state, err := scheduler.Snapshot(ctx)
				if err != nil {
					t.Error(err)
					return false
				}
				return len(state.Workers) == 1 && state.Workers[0].QueuedForeground+state.Workers[0].QueuedBackground == 5
			}, 5*time.Second, 10*time.Millisecond)
			require.NoError(t, scheduler.Release(ctx, hold))
			var order []admission.Priority
			for range 5 {
				select {
				case result := <-results:
					require.NoError(t, result.err)
					order = append(order, result.priority)
					require.NoError(t, scheduler.Release(ctx, result.permit))
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			require.Equal(t, admission.Autocomplete, order[0])
			require.Equal(t, admission.Background, order[burst])
			require.Equal(t, admission.Foreground, order[burst+1])
		})
	}
}
