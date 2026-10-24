package presence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/contenox/runtime/libkvstore"
)

type Kind string

const (
	KindACP         Kind = "acp"
	KindVSCodeAgent Kind = "vscode-agent"
	KindServe       Kind = "serve"
)

const DefaultTTL = 30 * time.Second

const DefaultHeartbeatInterval = DefaultTTL / 3

const DefaultInitialDelay = 1500 * time.Millisecond

const ReapMultiple = 3

type Record struct {
	InstanceID   string    `json:"instanceId"`
	Kind         Kind      `json:"kind"`
	PID          int       `json:"pid"`
	Host         string    `json:"host,omitempty"`
	StartedAt    time.Time `json:"startedAt"`
	LastSeen     time.Time `json:"lastSeen"`
	Cwd          string    `json:"cwd,omitempty"`
	Address      string    `json:"address,omitempty"`
	SessionCount int       `json:"sessionCount"`
	ClientName   string    `json:"clientName,omitempty"`
}

type Entry struct {
	Record
	External bool `json:"external"`
	Stale    bool `json:"stale"`
}

const keyPrefix = "presence:"

func recordKey(kind Kind, instanceID string) string {
	return keyPrefix + string(kind) + ":" + instanceID
}

type Store struct {
	kv       libkvstore.KVManager
	now      func() time.Time
	staleTTL time.Duration
	hardTTL  time.Duration
}

type StoreOption func(*Store)

func WithClock(now func() time.Time) StoreOption {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

func WithStaleTTL(d time.Duration) StoreOption {
	return func(s *Store) {
		if d > 0 {
			s.staleTTL = d
		}
	}
}

func WithHardTTL(d time.Duration) StoreOption {
	return func(s *Store) {
		if d > 0 {
			s.hardTTL = d
		}
	}
}

func NewStore(kv libkvstore.KVManager, opts ...StoreOption) *Store {
	s := &Store{
		kv:       kv,
		now:      func() time.Time { return time.Now() },
		staleTTL: DefaultTTL,
		hardTTL:  DefaultTTL * ReapMultiple,
	}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

func (s *Store) Register(ctx context.Context, rec Record) error {
	if rec.InstanceID == "" {
		return errors.New("presence: instanceID is required")
	}
	if rec.Kind == "" {
		return errors.New("presence: kind is required")
	}
	if rec.LastSeen.IsZero() {
		rec.LastSeen = s.now().UTC()
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = rec.LastSeen
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("presence: marshal record: %w", err)
	}
	exec, err := s.kv.Executor(ctx)
	if err != nil {
		return fmt.Errorf("presence: kv executor: %w", err)
	}
	if err := exec.SetWithTTL(ctx, recordKey(rec.Kind, rec.InstanceID), b, s.hardTTL); err != nil {
		return fmt.Errorf("presence: write record: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]Entry, error) {
	exec, err := s.kv.Executor(ctx)
	if err != nil {
		return nil, fmt.Errorf("presence: kv executor: %w", err)
	}
	keys, err := exec.Keys(ctx, keyPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("presence: list keys: %w", err)
	}
	now := s.now()
	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		raw, err := exec.Get(ctx, k)
		if errors.Is(err, libkvstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("presence: read %q: %w", k, err)
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		entries = append(entries, Entry{
			Record:   rec,
			External: true,
			Stale:    now.Sub(rec.LastSeen) > s.staleTTL,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		if !entries[i].StartedAt.Equal(entries[j].StartedAt) {
			return entries[i].StartedAt.Before(entries[j].StartedAt)
		}
		return entries[i].InstanceID < entries[j].InstanceID
	})
	return entries, nil
}

func (s *Store) Deregister(ctx context.Context, kind Kind, instanceID string) error {
	exec, err := s.kv.Executor(ctx)
	if err != nil {
		return fmt.Errorf("presence: kv executor: %w", err)
	}
	if err := exec.Delete(ctx, recordKey(kind, instanceID)); err != nil {
		return fmt.Errorf("presence: delete record: %w", err)
	}
	return nil
}

var _ ReporterStore = (*Store)(nil)
