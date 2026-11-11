package workspacetriggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libroutine"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

const (
	CursorConsumer = "workspacetriggers"
	DefaultMaxHop  = 4
)

type Starter interface {
	StartMission(ctx context.Context, run Run) (missionID string, err error)
}

type MissionOutcome struct {
	Terminal bool
	OK       bool
	Status   string
}

type MissionState func(ctx context.Context, missionID string) (MissionOutcome, error)

func RequestID(triggerID string, nid int64) string {
	return "trg-" + triggerID + "-" + strconv.FormatInt(nid, 10)
}

type DispatcherConfig struct {
	WorkspaceID string
	DB          libdb.DBManager
	Log         eventlog.Service
	Events      workspaceevents.Service
	Roots       workspaceevents.Roots
	Store       Store
	Starter     Starter
	Missions    MissionState
	Tracker     libtracker.ActivityTracker
	Reconcile   time.Duration
	Tick        time.Duration
	RunCheck    time.Duration
	Grace       time.Duration
	MaxRun      time.Duration
	MaxHop      int
	Batch       int
	Now         func() time.Time
}

func (c DispatcherConfig) withDefaults() DispatcherConfig {
	if c.Reconcile <= 0 {
		c.Reconcile = 2 * time.Second
	}
	if c.Tick <= 0 {
		c.Tick = 250 * time.Millisecond
	}
	if c.RunCheck <= 0 {
		c.RunCheck = 2 * time.Second
	}
	if c.Grace <= 0 {
		c.Grace = 5 * time.Second
	}
	if c.MaxRun <= 0 {
		c.MaxRun = 30 * time.Minute
	}
	if c.MaxHop <= 0 {
		c.MaxHop = DefaultMaxHop
	}
	if c.Batch <= 0 {
		c.Batch = 256
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Tracker == nil {
		c.Tracker = libtracker.NoopTracker{}
	}
	return c
}

type Dispatcher struct {
	cfg     DispatcherConfig
	history History

	mu      sync.Mutex
	workers map[string]*rootWorker
	scopes  map[string]*rootWorker
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	cfg = cfg.withDefaults()
	return &Dispatcher{
		cfg:     cfg,
		history: History{DB: cfg.DB, Log: cfg.Log, Store: cfg.Store, Now: cfg.Now},
		workers: map[string]*rootWorker{},
		scopes:  map[string]*rootWorker{},
	}
}

func (d *Dispatcher) Start(ctx context.Context) {
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	d.mu.Unlock()
	go d.run(runCtx)
}

func (d *Dispatcher) Stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (d *Dispatcher) Active(root string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.workers[root]
	return ok
}

func (d *Dispatcher) HandleEvent(_ context.Context, events ...*runtimetypes.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range events {
		if e == nil {
			continue
		}
		if w := d.scopes[e.WorkspaceID]; w != nil {
			w.nudge()
		}
	}
}

func (d *Dispatcher) run(ctx context.Context) {
	defer close(d.done)
	ticker := time.NewTicker(d.cfg.Reconcile)
	defer ticker.Stop()
	for {
		d.reconcile(ctx)
		select {
		case <-ctx.Done():
			d.stopAll()
			return
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) reconcile(ctx context.Context) {
	granted := map[string]bool{}
	for _, root := range d.cfg.Roots.Roots() {
		granted[root] = true
		d.mu.Lock()
		_, running := d.workers[root]
		d.mu.Unlock()
		if !running {
			d.startWorker(ctx, root)
		}
	}
	d.mu.Lock()
	var stale []*rootWorker
	for root, w := range d.workers {
		if !granted[root] {
			stale = append(stale, w)
		}
	}
	d.mu.Unlock()
	for _, w := range stale {
		d.stopWorker(w)
	}
}

func (d *Dispatcher) startWorker(ctx context.Context, root string) {
	workerCtx, cancel := context.WithCancel(ctx)
	w := &rootWorker{
		d:      d,
		root:   root,
		scope:  workspaceevents.Scope(d.cfg.WorkspaceID, root),
		wake:   make(chan struct{}, 1),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	d.mu.Lock()
	d.workers[root] = w
	d.scopes[w.scope] = w
	d.mu.Unlock()
	go w.loop(workerCtx)
}

func (d *Dispatcher) stopWorker(w *rootWorker) {
	w.cancel()
	<-w.done
	d.mu.Lock()
	if d.workers[w.root] == w {
		delete(d.workers, w.root)
		delete(d.scopes, w.scope)
	}
	d.mu.Unlock()
}

func (d *Dispatcher) stopAll() {
	d.mu.Lock()
	all := make([]*rootWorker, 0, len(d.workers))
	for _, w := range d.workers {
		all = append(all, w)
	}
	d.mu.Unlock()
	for _, w := range all {
		d.stopWorker(w)
	}
}

func (d *Dispatcher) report(ctx context.Context, operation, root string, err error) {
	reportErr, _, end := d.cfg.Tracker.Start(ctx, operation, "workspace_trigger", "root", root)
	reportErr(err)
	end()
}

type rootWorker struct {
	d            *Dispatcher
	root         string
	scope        string
	wake         chan struct{}
	cancel       context.CancelFunc
	done         chan struct{}
	lastRunCheck time.Time
}

func (w *rootWorker) nudge() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *rootWorker) loop(ctx context.Context) {
	defer close(w.done)
	bus := make(chan []byte, 16)
	if sub, err := w.d.cfg.Events.Subscribe(ctx, w.root, bus); err == nil {
		defer func() { _ = sub.Unsubscribe() }()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, open := <-bus:
					if !open {
						return
					}
					w.nudge()
				}
			}
		}()
	} else {
		w.d.report(ctx, "subscribe", w.root, fmt.Errorf("workspacetriggers: live wake-ups unavailable, polling instead: %w", err))
	}
	routine := libroutine.NewRoutine(5, 30*time.Second)
	routine.Loop(ctx, w.d.cfg.Tick, w.wake, w.pass, func(err error) {
		if !errors.Is(err, libroutine.ErrCircuitOpen) && ctx.Err() == nil {
			w.d.report(ctx, "dispatch", w.root, err)
		}
	})
}

func (w *rootWorker) firings() (runtimetypes.EventFiringStore, error) {
	return runtimetypes.NewEventFiringStore(w.d.cfg.DB.WithoutTransaction(), w.scope)
}

func (w *rootWorker) pass(ctx context.Context) error {
	cfg := w.d.cfg
	firings, err := w.firings()
	if err != nil {
		return err
	}
	cursor, err := firings.GetEventCursor(ctx, CursorConsumer)
	if err != nil {
		return err
	}
	list, err := cfg.Store.List(ctx, w.scope)
	if err != nil {
		return err
	}
	triggers, compileErr := compileTriggers(list)
	if compileErr != nil {
		w.d.report(ctx, "compile", w.root, compileErr)
	}
	recent, err := firings.ListEventFirings(ctx, runtimetypes.EventFiringFilter{Limit: runtimetypes.MaxEventFiringLimit})
	if err != nil {
		return err
	}
	events, err := w.read(ctx, cursor)
	if err != nil {
		return err
	}
	now := cfg.Now()
	p := planPass(planInput{
		events:   events,
		triggers: triggers,
		paused:   pauseWindows(recent),
		now:      now,
		grace:    cfg.Grace,
		maxRun:   cfg.MaxRun,
		maxHop:   cfg.MaxHop,
	})
	for _, r := range p.refused {
		if err := w.refuse(ctx, firings, r); err != nil {
			return err
		}
	}
	through := p.settledThrough
	for _, b := range p.due {
		delivered, err := w.deliver(ctx, firings, b)
		if err != nil {
			return err
		}
		if !delivered && b.firstNID-1 < through {
			through = b.firstNID - 1
		}
	}
	if through > cursor {
		if err := firings.SetEventCursor(context.WithoutCancel(ctx), CursorConsumer, through); err != nil {
			return err
		}
	}
	if now.Sub(w.lastRunCheck) >= cfg.RunCheck {
		w.lastRunCheck = now
		return w.settle(ctx, firings)
	}
	return nil
}

func (w *rootWorker) read(ctx context.Context, cursor int64) ([]workspaceevents.Event, error) {
	var out []workspaceevents.Event
	from := cursor
	for len(out) < 20*w.d.cfg.Batch {
		page, err := w.d.cfg.Events.Since(ctx, w.root, from, w.d.cfg.Batch)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < w.d.cfg.Batch {
			break
		}
		from = page[len(page)-1].NID
	}
	return out, nil
}

func pauseWindows(firings []runtimetypes.EventFiring) []window {
	out := make([]window, 0, len(firings))
	for _, f := range firings {
		if f.Status == runtimetypes.EventFiringStatusRefused {
			continue
		}
		if f.Status == runtimetypes.EventFiringStatusRunning {
			out = append(out, window{start: f.CreatedAt})
			continue
		}
		out = append(out, window{start: f.CreatedAt, end: f.UpdatedAt})
	}
	return out
}

func (w *rootWorker) refuse(ctx context.Context, firings runtimetypes.EventFiringStore, r refusal) error {
	claimed, err := firings.BeginEventFiring(ctx, r.trigger.ID, r.nid, RequestID(r.trigger.ID, r.nid))
	if err != nil || !claimed {
		return err
	}
	msg := fmt.Sprintf("event %d is %d hops from a person; the limit is %d", r.nid, r.hop, w.d.cfg.MaxHop)
	return firings.FinishEventFiring(context.WithoutCancel(ctx), r.trigger.ID, r.nid, runtimetypes.EventFiringStatusRefused, msg)
}

func (w *rootWorker) deliver(ctx context.Context, firings runtimetypes.EventFiringStore, b *burst) (bool, error) {
	requestID := RequestID(b.trigger.ID, b.lastNID)
	claimed, err := firings.BeginEventFiring(ctx, b.trigger.ID, b.lastNID, requestID)
	if err != nil {
		return false, err
	}
	if !claimed {
		return w.handled(ctx, firings, b.trigger.ID, b.lastNID)
	}
	runCtx := runtimetypes.WithEventHop(context.WithValue(ctx, libtracker.ContextKeyRequestID, requestID), b.hop+1)
	missionID, startErr := w.d.cfg.Starter.StartMission(runCtx, b.run(w.root))
	if startErr != nil {
		if err := firings.FinishEventFiring(context.WithoutCancel(ctx), b.trigger.ID, b.lastNID, runtimetypes.EventFiringStatusError, startErr.Error()); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := w.record(runCtx, workspaceevents.TypeTriggerStarted, requestID, map[string]any{
		"trigger": b.trigger.ID, "triggerName": b.trigger.Name, "nid": b.lastNID, "missionId": missionID,
	}); err != nil {
		w.d.report(ctx, "record_start", w.root, fmt.Errorf("workspacetriggers: mission %s started for %s but its start was not recorded: %w", missionID, requestID, err))
	}
	return true, nil
}

func (w *rootWorker) handled(ctx context.Context, firings runtimetypes.EventFiringStore, triggerID string, nid int64) (bool, error) {
	f, found, err := findFiring(ctx, firings, triggerID, nid)
	if err != nil || !found {
		return false, err
	}
	if f.Status != runtimetypes.EventFiringStatusRunning {
		return true, nil
	}
	rec, err := w.d.history.started(ctx, w.scope, f)
	return rec.MissionID != "", err
}

func findFiring(ctx context.Context, firings runtimetypes.EventFiringStore, triggerID string, nid int64) (runtimetypes.EventFiring, bool, error) {
	list, err := firings.ListEventFirings(ctx, runtimetypes.EventFiringFilter{SinceNID: nid - 1, TriggerName: triggerID, Limit: runtimetypes.MaxEventFiringLimit})
	if err != nil {
		return runtimetypes.EventFiring{}, false, err
	}
	for _, f := range list {
		if f.NID == nid {
			return f, true, nil
		}
	}
	return runtimetypes.EventFiring{}, false, nil
}

func (w *rootWorker) settle(ctx context.Context, firings runtimetypes.EventFiringStore) error {
	running, err := firings.ListEventFirings(ctx, runtimetypes.EventFiringFilter{Status: runtimetypes.EventFiringStatusRunning, Limit: runtimetypes.MaxEventFiringLimit})
	if err != nil {
		return err
	}
	var failures []error
	for _, f := range running {
		rec, err := w.d.history.started(ctx, w.scope, f)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		missionID := rec.MissionID
		if missionID == "" {
			continue
		}
		outcome, err := w.d.cfg.Missions(ctx, missionID)
		if err != nil {
			failures = append(failures, fmt.Errorf("workspacetriggers: mission %s: %w", missionID, err))
			continue
		}
		if !outcome.Terminal {
			continue
		}
		status, msg := runtimetypes.EventFiringStatusOK, ""
		if !outcome.OK {
			status, msg = runtimetypes.EventFiringStatusError, "mission "+outcome.Status
		}
		if err := firings.FinishEventFiring(context.WithoutCancel(ctx), f.TriggerName, f.NID, status, msg); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := w.record(ctx, workspaceevents.TypeTriggerSettled, f.RequestID, map[string]any{
			"trigger": f.TriggerName, "nid": f.NID, "missionId": missionID, "status": status, "missionStatus": outcome.Status,
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *rootWorker) record(ctx context.Context, eventType, requestID string, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.d.cfg.Events.Append(context.WithoutCancel(ctx), w.root, workspaceevents.Input{
		Type:    eventType,
		Source:  workspaceevents.SourceTrigger,
		Subject: requestID,
		Data:    raw,
	})
	return err
}
