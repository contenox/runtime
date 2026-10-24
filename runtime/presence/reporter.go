package presence

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ReporterStore interface {
	Register(ctx context.Context, rec Record) error
	Deregister(ctx context.Context, kind Kind, instanceID string) error
}

type Reporter struct {
	store        ReporterStore
	log          *slog.Logger
	interval     time.Duration
	initialDelay time.Duration

	mu  sync.Mutex
	rec Record

	kick     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

type ReporterOption func(*Reporter)

func WithInterval(d time.Duration) ReporterOption {
	return func(r *Reporter) {
		if d > 0 {
			r.interval = d
		}
	}
}

func WithLogger(l *slog.Logger) ReporterOption {
	return func(r *Reporter) {
		if l != nil {
			r.log = l
		}
	}
}

func StartReporter(ctx context.Context, store ReporterStore, rec Record, opts ...ReporterOption) *Reporter {
	rctx, cancel := context.WithCancel(ctx)
	r := &Reporter{
		store:        store,
		log:          slog.Default(),
		interval:     DefaultHeartbeatInterval,
		initialDelay: DefaultInitialDelay,
		rec:          rec,
		kick:         make(chan struct{}, 1),
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	if r.rec.InstanceID == "" {
		r.rec.InstanceID = uuid.NewString()
	}
	if r.rec.PID == 0 {
		r.rec.PID = os.Getpid()
	}
	if r.rec.Host == "" {
		r.rec.Host, _ = os.Hostname()
	}
	if r.rec.StartedAt.IsZero() {
		r.rec.StartedAt = time.Now().UTC()
	}
	go r.run(rctx)
	return r
}

func (r *Reporter) InstanceID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.InstanceID
}

func (r *Reporter) Update(mutate func(rec *Record)) {
	if mutate == nil {
		return
	}
	r.mu.Lock()
	mutate(&r.rec)
	r.mu.Unlock()
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

func (r *Reporter) Stop() {
	r.stopOnce.Do(r.cancel)
	<-r.done
}

func (r *Reporter) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	select {
	case <-ctx.Done():
		return
	case <-time.After(r.initialDelay):
	case <-r.kick:
	}
	r.write(ctx)
	for {
		select {
		case <-ctx.Done():
			r.deregister()
			return
		case <-ticker.C:
			r.write(ctx)
		case <-r.kick:
			r.write(ctx)
		}
	}
}

func (r *Reporter) write(ctx context.Context) {
	r.mu.Lock()
	rec := r.rec
	r.mu.Unlock()
	rec.LastSeen = time.Now().UTC()
	if err := r.store.Register(ctx, rec); err != nil {
		r.log.Debug("presence: heartbeat write failed (ignored)", "kind", rec.Kind, "instance", rec.InstanceID, "err", err)
	}
}

func (r *Reporter) deregister() {
	r.mu.Lock()
	kind, id := r.rec.Kind, r.rec.InstanceID
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.store.Deregister(ctx, kind, id); err != nil {
		r.log.Debug("presence: deregister failed (ignored)", "kind", kind, "instance", id, "err", err)
	}
}

func WithInitialDelay(d time.Duration) ReporterOption {
	return func(r *Reporter) { r.initialDelay = d }
}
