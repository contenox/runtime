package admission_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_EventScheduler_ConcurrentFirstTransactionsRegisterOnePartition(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := admission.Config{QueueCapacity: 4, ForegroundBurst: 2}
	open := func() (*admission.EventScheduler, libdbexec.DBManager, libbus.Messenger) {
		db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
		require.NoError(t, err)
		bus := libbus.NewSQLite(db.WithoutTransaction())
		scheduler, err := admission.NewEventScheduler(ctx, db, bus, config, func(err error) { t.Errorf("admission background failure: %v", err) })
		require.NoError(t, err)
		return scheduler, db, bus
	}
	const connections = 8
	schedulers := make([]*admission.EventScheduler, connections)
	dbs := make([]libdbexec.DBManager, connections)
	buses := make([]libbus.Messenger, connections)
	for i := range connections {
		schedulers[i], dbs[i], buses[i] = open()
	}
	t.Cleanup(func() {
		for i := range connections {
			require.NoError(t, errors.Join(buses[i].Close(), dbs[i].Close()))
		}
	})
	start := make(chan struct{})
	errs := make(chan error, connections)
	var wg sync.WaitGroup
	for _, scheduler := range schedulers {
		wg.Add(1)
		go func(scheduler *admission.EventScheduler) {
			defer wg.Done()
			<-start
			for range 8 {
				if err := scheduler.Configure(ctx, config); err != nil {
					errs <- err
					return
				}
			}
		}(scheduler)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	parts, err := runtimetypes.NewEventStore(dbs[0]).ListEventPartitions(ctx)
	require.NoError(t, err)
	require.Len(t, parts, 1)
}
