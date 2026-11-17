package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libroutine"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const projectionSchema = `
CREATE TABLE IF NOT EXISTS modeld_admission(id INTEGER PRIMARY KEY CHECK(id=1), config TEXT NOT NULL, state TEXT NOT NULL);
`

const admissionWorkspace = "modeld-host"
const admissionWake = "modeld.admission.changed"

// EventScheduler derives admission from the host event log and an atomic checkpoint.
// Separate instances share database transactions, not process-local locks or queues.
type EventScheduler struct {
	events runtimetypes.EventStore
	db     libdbexec.DBManager
	bus    libbus.Messenger
	report func(error)
}

// Run drains admission transitions on bus notifications and a polling backstop.
// Multiple instances may run concurrently against the same persisted projection.
func (s *EventScheduler) Run(ctx context.Context) (err error) {
	wake := make(chan []byte, 1)
	sub, err := s.bus.Stream(ctx, admissionWake, wake)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sub.Unsubscribe()) }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	breaker := libroutine.NewRoutine(3, time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-tick.C:
		}
		if !breaker.Allow() {
			continue
		}
		if err := s.transact(ctx, s.dispatch); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			breaker.MarkFailure()
			s.report(err)
		} else {
			breaker.MarkSuccess()
		}
	}
}

type admissionState struct {
	Plans           map[string]Allocation
	Allocations     map[string]Allocation
	Protections     []Protection
	Ceilings        map[string]float64
	Cursor          int64
	Workers         map[string]Worker
	Requests        map[string]eventRequest
	Burst           int
	CompletionBurst int
}

type eventRequest struct {
	Suspended      bool
	YieldRequested bool
	Cancellation   string
	Request        Request
	Sequence       uint64
	Deadline       int64
	Permit         *Permit
	Accepted       bool
	Failure        string
}

type admissionChange struct {
	Plan            *Allocation         `json:",omitempty"`
	Allocation      *Allocation         `json:",omitempty"`
	Protections     *[]Protection       `json:",omitempty"`
	Ceilings        *map[string]float64 `json:",omitempty"`
	Policy          *Config             `json:",omitempty"`
	Worker          *Worker             `json:",omitempty"`
	ID              string              `json:",omitempty"`
	Request         *eventRequest       `json:",omitempty"`
	Delete          bool                `json:",omitempty"`
	Burst           *int                `json:",omitempty"`
	CompletionBurst *int                `json:",omitempty"`
}

type eventTransaction struct {
	config  Config
	ctx     context.Context
	exec    libdbexec.Exec
	state   admissionState
	changed bool
}

type borrowedDB struct{ libdbexec.Exec }

func (d borrowedDB) WithoutTransaction() libdbexec.Exec { return d.Exec }
func (d borrowedDB) Close() error                       { return nil }
func (d borrowedDB) WithTransaction(context.Context, ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	return nil, nil, nil, fmt.Errorf("admission: nested transaction is not supported")
}
func (d borrowedDB) WithTransactionOptions(context.Context, *sql.TxOptions, ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	return nil, nil, nil, fmt.Errorf("admission: nested transaction is not supported")
}

// NewEventScheduler borrows the existing runtime event database and bus.
// Instances sharing that database share admission; report receives failed wakeups.
func NewEventScheduler(ctx context.Context, db libdbexec.DBManager, bus libbus.Messenger, config Config, report func(error)) (*EventScheduler, error) {
	if db == nil || bus == nil || report == nil {
		return nil, fmt.Errorf("admission: database, bus and error reporter required")
	}
	if config.QueueCapacity <= 0 || config.GlobalLimit < 0 || config.ForegroundBurst <= 0 || config.MaxQueueWait < 0 {
		return nil, fmt.Errorf("admission: invalid bounds")
	}
	if _, err := db.WithoutTransaction().ExecContext(ctx, projectionSchema); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	state, err := json.Marshal(admissionState{Workers: map[string]Worker{}, Requests: map[string]eventRequest{}})
	if err != nil {
		return nil, err
	}
	if _, err := db.WithoutTransaction().ExecContext(ctx, `INSERT INTO modeld_admission(id,config,state) VALUES(1,$1,$2) ON CONFLICT(id) DO NOTHING`, string(raw), string(state)); err != nil {
		return nil, err
	}
	var existing string
	if err := db.WithoutTransaction().QueryRowContext(ctx, `SELECT config FROM modeld_admission WHERE id=1`).Scan(&existing); err != nil {
		return nil, err
	}
	if existing != string(raw) {
		return nil, fmt.Errorf("admission: host policy differs from requested policy")
	}
	return &EventScheduler{events: runtimetypes.NewEventStore(db), db: db, bus: bus, report: report}, nil
}

// Configure records shared admission policy without interrupting accepted execution.
func (s *EventScheduler) Configure(ctx context.Context, policy Config) error {
	if policy.QueueCapacity <= 0 || policy.GlobalLimit < 0 || policy.ForegroundBurst <= 0 || policy.MaxQueueWait < 0 {
		return fmt.Errorf("admission: invalid bounds")
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		if err := t.append("policy_changed", admissionChange{Policy: &policy}); err != nil {
			return err
		}
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if _, err := t.exec.ExecContext(ctx, `UPDATE modeld_admission SET config=$1 WHERE id=1`, string(raw)); err != nil {
			return err
		}
		if err := clikv.SetString(ctx, runtimetypes.New(t.exec), "inference.modeld.admission", string(raw)); err != nil {
			return err
		}
		return s.dispatch(t)
	})
}

func (s *EventScheduler) transact(ctx context.Context, fn func(*eventTransaction) error) (err error) {
	if err := s.events.EnsureEventPartitionExists(ctx, time.Now()); err != nil {
		return err
	}
	exec, commit, rollback, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rollback()) }()
	if _, err = exec.ExecContext(ctx, `UPDATE modeld_admission SET id=id WHERE id=1`); err != nil {
		return err
	}
	var raw, policy string
	if err = exec.QueryRowContext(ctx, `SELECT state,config FROM modeld_admission WHERE id=1`).Scan(&raw, &policy); err != nil {
		return err
	}
	t := &eventTransaction{ctx: ctx, exec: exec}
	if err = json.Unmarshal([]byte(policy), &t.config); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(raw), &t.state); err != nil {
		return err
	}
	store := runtimetypes.NewEventStore(borrowedDB{exec})
	for {
		events, readErr := store.ListEventsSince(ctx, admissionWorkspace, t.state.Cursor, runtimetypes.MaxEventListLimit)
		if readErr != nil {
			return readErr
		}
		for _, ev := range events {
			var change admissionChange
			if err := json.Unmarshal(ev.Data, &change); err != nil {
				return err
			}
			t.apply(change)
			t.state.Cursor = ev.NID
		}
		if len(events) < runtimetypes.MaxEventListLimit {
			break
		}
	}
	if err = fn(t); err != nil {
		return err
	}
	encoded, err := json.Marshal(t.state)
	if err != nil {
		return err
	}
	if _, err = exec.ExecContext(ctx, `UPDATE modeld_admission SET state=$1 WHERE id=1`, string(encoded)); err != nil {
		return err
	}
	if err = commit(ctx); err != nil {
		return err
	}
	if t.changed {
		wakeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := s.bus.Publish(wakeCtx, admissionWake, []byte{}); err != nil {
			s.report(err)
		}
	}
	return nil
}

func (t *eventTransaction) apply(c admissionChange) {
	if t.state.Plans == nil {
		t.state.Plans = map[string]Allocation{}
	}
	if c.Plan != nil {
		t.state.Plans[c.Plan.WorkerID] = *c.Plan
	}
	if t.state.Allocations == nil {
		t.state.Allocations = map[string]Allocation{}
	}
	if c.Allocation != nil {
		t.state.Allocations[c.Allocation.WorkerID] = *c.Allocation
	}
	if c.Protections != nil {
		t.state.Protections = *c.Protections
	}
	if c.Ceilings != nil {
		t.state.Ceilings = *c.Ceilings
	}
	if c.Policy != nil {
		t.config = *c.Policy
	}
	if t.state.Workers == nil {
		t.state.Workers = map[string]Worker{}
	}
	if t.state.Requests == nil {
		t.state.Requests = map[string]eventRequest{}
	}
	if c.Worker != nil {
		t.state.Workers[c.Worker.ID] = *c.Worker
		if c.Worker.Stopped {
			delete(t.state.Allocations, c.Worker.ID)
			delete(t.state.Plans, c.Worker.ID)
		}
	}
	if c.Request != nil {
		t.state.Requests[c.ID] = *c.Request
	}
	if c.Delete {
		delete(t.state.Requests, c.ID)
	}
	if c.Burst != nil {
		t.state.Burst = *c.Burst
	}
	if c.CompletionBurst != nil {
		t.state.CompletionBurst = *c.CompletionBurst
	}
}

func (t *eventTransaction) append(kind string, c admissionChange) error {
	if kind == "requested" {
		var sequence uint64
		if err := t.exec.QueryRowContext(t.ctx, `SELECT last_nid+1 FROM event_nid_seq WHERE id=1`).Scan(&sequence); err != nil {
			return err
		}
		c.Request.Sequence = sequence
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	ev := runtimetypes.Event{WorkspaceID: admissionWorkspace, Type: "modeld.admission." + kind, Subject: c.ID, Data: raw}
	if err := runtimetypes.AppendEventInTransaction(t.ctx, t.exec, &ev); err != nil {
		return err
	}
	t.apply(c)
	t.state.Cursor = ev.NID
	t.changed = true
	return nil
}

func requestFailure(w Worker, r Request) error {
	if w.OwnerEpoch != r.OwnerEpoch {
		return ErrOwnerEpoch
	}
	if w.Draining {
		return ErrDraining
	}
	if !w.Ready {
		return ErrNotReady
	}
	return nil
}

func storedFailure(raw string) error {
	for _, err := range []error{ErrQueueFull, ErrSuperseded, ErrWorkerNotFound, ErrOwnerEpoch, ErrNotReady, ErrDraining} {
		if err.Error() == raw {
			return err
		}
	}
	return errors.New(raw)
}

func (s *EventScheduler) register(ctx context.Context, w Worker, update bool) error {
	if w.Stopped && w.Ready {
		return fmt.Errorf("admission: stopped worker cannot be ready")
	}
	if w.ID == "" || w.OwnerEpoch == "" {
		return fmt.Errorf("admission: worker ID and epoch required")
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		old, exists := t.state.Workers[w.ID]
		if exists && old.Stopped && old.OwnerEpoch == w.OwnerEpoch && !w.Stopped {
			return ErrOwnerEpoch
		}
		if update && !exists {
			return ErrWorkerNotFound
		}
		if update && old.OwnerEpoch != w.OwnerEpoch {
			return ErrOwnerEpoch
		}
		if exists && old.OwnerEpoch != w.OwnerEpoch {
			if _, resident := t.state.Allocations[w.ID]; resident {
				return ErrWorkerBusy
			}
			for _, r := range t.state.Requests {
				if r.Request.WorkerID == w.ID && r.Permit != nil {
					return ErrWorkerBusy
				}
			}
		}
		if !exists || old != w {
			if err := t.append("worker_changed", admissionChange{Worker: &w}); err != nil {
				return err
			}
		}
		return s.dispatch(t)
	})
}

// Register records a worker's current owner and readiness.
func (s *EventScheduler) Register(ctx context.Context, w Worker) error {
	return s.register(ctx, w, false)
}

// Update changes readiness without replacing the worker's owner.
func (s *EventScheduler) Update(ctx context.Context, w Worker) error { return s.register(ctx, w, true) }

func (s *EventScheduler) dispatch(t *eventTransaction) error {
	now := time.Now().UnixNano()
	active := map[string]bool{}
	occupied := map[string]bool{}
	var waiting []string
	for id, r := range t.state.Requests {
		if w, exists := t.state.Workers[r.Request.WorkerID]; exists && w.OwnerEpoch == r.Request.OwnerEpoch && w.Stopped && r.Permit != nil {
			if err := t.append("worker_stopped", admissionChange{ID: id, Delete: true}); err != nil {
				return err
			}
			continue
		}
		if r.Permit != nil && r.Accepted {
			occupied[r.Request.WorkerID] = true
			if r.Suspended {
				waiting = append(waiting, id)
			} else {
				active[r.Request.WorkerID] = true
			}
			continue
		}
		if r.Deadline != 0 && r.Deadline <= now {
			if err := t.append("expired", admissionChange{ID: id, Delete: true}); err != nil {
				return err
			}
			continue
		}
		if r.Permit != nil {
			active[r.Request.WorkerID] = true
			occupied[r.Request.WorkerID] = true
			continue
		}
		if r.Failure != "" {
			continue
		}
		w, exists := t.state.Workers[r.Request.WorkerID]
		err := requestFailure(w, r.Request)
		if !exists {
			err = ErrWorkerNotFound
		}
		if err != nil {
			r.Failure = err.Error()
			if err := t.append("rejected", admissionChange{ID: id, Request: &r}); err != nil {
				return err
			}
			continue
		}
		waiting = append(waiting, id)
	}
	sort.Slice(waiting, func(i, j int) bool {
		return t.state.Requests[waiting[i]].Sequence < t.state.Requests[waiting[j]].Sequence
	})
	for t.config.GlobalLimit == 0 || len(active) < t.config.GlobalLimit {
		chosen := ""
		priorities := t.priorities()
		for _, priority := range priorities {
			for _, id := range waiting {
				r := t.state.Requests[id]
				if !active[r.Request.WorkerID] && (!occupied[r.Request.WorkerID] || r.Suspended) && r.Request.Priority == priority && t.computeFits(r.Request.WorkerID, active) {
					chosen = id
					break
				}
			}
			if chosen != "" {
				break
			}
		}
		if chosen == "" {
			break
		}
		r := t.state.Requests[chosen]
		if r.Permit == nil {
			r.Permit = &Permit{ID: r.Sequence, WorkerID: r.Request.WorkerID, OwnerEpoch: r.Request.OwnerEpoch}
		}
		r.Suspended, r.YieldRequested = false, false
		burst := 0
		if r.Request.Priority != Background {
			burst = t.state.Burst + 1
		}
		completionBurst := t.state.CompletionBurst
		if r.Request.Priority == Autocomplete {
			completionBurst = t.state.CompletionBurst + 1
		} else if r.Request.Priority == Foreground {
			completionBurst = 0
		}
		if err := t.append("admitted", admissionChange{ID: chosen, Request: &r, Burst: &burst, CompletionBurst: &completionBurst}); err != nil {
			return err
		}
		active[r.Request.WorkerID] = true
		occupied[r.Request.WorkerID] = true
	}
	return t.requestYields(active, waiting)
}

// Acquire durably queues work and atomically accepts a grant before returning it.
func (s *EventScheduler) Acquire(ctx context.Context, r Request) (Permit, error) {
	if err := ctx.Err(); err != nil {
		return Permit{}, err
	}
	if r.Priority != Autocomplete && r.Priority != Foreground && r.Priority != Background {
		return Permit{}, fmt.Errorf("admission: invalid priority")
	}
	if r.Supersede && r.RequesterID == "" {
		return Permit{}, fmt.Errorf("admission: supersession requires requester ID")
	}
	id := uuid.NewString()
	deadlineNanos := int64(0)
	if deadline, ok := ctx.Deadline(); ok {
		deadlineNanos = deadline.UnixNano()
	}
	err := s.transact(ctx, func(t *eventTransaction) error {
		if t.config.MaxQueueWait > 0 {
			maximum := time.Now().Add(t.config.MaxQueueWait).UnixNano()
			if deadlineNanos == 0 || maximum < deadlineNanos {
				deadlineNanos = maximum
			}
		}
		if err := s.dispatch(t); err != nil {
			return err
		}
		w, exists := t.state.Workers[r.WorkerID]
		if !exists {
			return ErrWorkerNotFound
		}
		if err := requestFailure(w, r); err != nil {
			return err
		}
		count := 0
		for priorID, prior := range t.state.Requests {
			if prior.Failure != "" {
				continue
			}
			sameWorker := prior.Request.WorkerID == r.WorkerID
			completion := r.Priority == Autocomplete && prior.Request.Priority == Autocomplete
			if r.Supersede && prior.Request.RequesterID == r.RequesterID && (sameWorker || completion) && (prior.Permit == nil || !prior.Accepted || completion) {
				if prior.Accepted {
					prior.Cancellation = ErrSuperseded.Error()
				} else {
					prior.Permit = nil
					prior.Failure = ErrSuperseded.Error()
				}
				if err := t.append("superseded", admissionChange{ID: priorID, Request: &prior}); err != nil {
					return err
				}
			} else if sameWorker && prior.Permit == nil {
				count++
			}
		}
		if count >= t.config.QueueCapacity {
			return ErrQueueFull
		}
		entry := eventRequest{Request: r, Deadline: deadlineNanos}
		if err := t.append("requested", admissionChange{ID: id, Request: &entry}); err != nil {
			return err
		}
		return s.dispatch(t)
	})
	if err != nil {
		return Permit{}, err
	}
	wake := make(chan []byte, 1)
	sub, err := s.bus.Stream(ctx, admissionWake, wake)
	if err != nil {
		return Permit{}, errors.Join(err, s.cancel(id))
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			s.report(err)
		}
	}()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		var permit Permit
		var failure error
		err = s.transact(ctx, func(t *eventTransaction) error {
			if err := s.dispatch(t); err != nil {
				return err
			}
			entry, exists := t.state.Requests[id]
			if !exists {
				failure = context.DeadlineExceeded
				return nil
			}
			if entry.Failure != "" {
				failure = storedFailure(entry.Failure)
				return t.append("observed", admissionChange{ID: id, Delete: true})
			}
			if entry.Permit != nil {
				entry.Accepted = true
				permit = *entry.Permit
				return t.append("accepted", admissionChange{ID: id, Request: &entry})
			}
			return nil
		})
		if err != nil {
			return Permit{}, errors.Join(err, s.cancel(id))
		}
		if failure != nil {
			return Permit{}, failure
		}
		if permit.ID != 0 {
			if err := ctx.Err(); err != nil {
				return Permit{}, errors.Join(err, s.cancel(id))
			}
			return permit, nil
		}
		select {
		case <-ctx.Done():
			return Permit{}, errors.Join(ctx.Err(), s.cancel(id))
		case <-wake:
		case <-tick.C:
		}
	}
}

// WaitCancellation observes cancellation without releasing the permit. A nil
// result means execution was released or its worker was confirmed stopped.
func (s *EventScheduler) WaitCancellation(ctx context.Context, permit Permit) error {
	if permit.ID == 0 || permit.WorkerID == "" || permit.OwnerEpoch == "" {
		return ErrInvalidPermit
	}
	wake := make(chan []byte, 1)
	sub, err := s.bus.Stream(ctx, admissionWake, wake)
	if err != nil {
		return err
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			s.report(err)
		}
	}()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		found := false
		var failure error
		if err := s.transact(ctx, func(t *eventTransaction) error {
			if permit.ID > uint64(t.state.Cursor) {
				return ErrInvalidPermit
			}
			for _, request := range t.state.Requests {
				if request.Permit != nil && *request.Permit == permit {
					found = true
					if request.Cancellation != "" {
						failure = storedFailure(request.Cancellation)
					}
					break
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if failure != nil || !found {
			return failure
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-tick.C:
		}
	}
}

func (s *EventScheduler) cancel(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.transact(ctx, func(t *eventTransaction) error {
		if _, exists := t.state.Requests[id]; exists {
			if err := t.append("cancelled", admissionChange{ID: id, Delete: true}); err != nil {
				return err
			}
		}
		return s.dispatch(t)
	})
}

// Release records completion; an old permit cannot release a newer grant.
func (s *EventScheduler) Release(ctx context.Context, p Permit) error {
	if p.ID == 0 || p.WorkerID == "" || p.OwnerEpoch == "" {
		return ErrInvalidPermit
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		if p.ID > uint64(t.state.Cursor) {
			return ErrInvalidPermit
		}
		for id, r := range t.state.Requests {
			if r.Permit != nil && *r.Permit == p {
				if err := t.append("released", admissionChange{ID: id, Delete: true}); err != nil {
					return err
				}
				break
			}
		}
		return s.dispatch(t)
	})
}

// Snapshot reads the shared projection after processing eligible transitions.
func (s *EventScheduler) Snapshot(ctx context.Context) (Snapshot, error) {
	result := Snapshot{}
	err := s.transact(ctx, func(t *eventTransaction) error {
		result.GlobalLimit = t.config.GlobalLimit
		result.Policy = t.config
		result.Resources = t.resourceSnapshot()
		if err := s.dispatch(t); err != nil {
			return err
		}
		for _, w := range t.state.Workers {
			entry := WorkerSnapshot{Worker: w}
			for _, r := range t.state.Requests {
				if r.Request.WorkerID != w.ID || r.Failure != "" {
					continue
				}
				if r.Permit != nil {
					p := *r.Permit
					entry.Active = &p
					entry.CancellationRequested = r.Cancellation != ""
					entry.Suspended, entry.YieldRequested = r.Suspended, r.YieldRequested
					result.Active++
				} else if r.Request.Priority != Background {
					entry.QueuedForeground++
				} else {
					entry.QueuedBackground++
				}
			}
			result.Workers = append(result.Workers, entry)
		}
		sort.Slice(result.Workers, func(i, j int) bool { return result.Workers[i].ID < result.Workers[j].ID })
		return nil
	})
	return result, err
}
