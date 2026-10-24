package operatorinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const inboxKVPrefix = "fleet:operator_inbox:"

type Reason string

const (
	ReasonOperatorFired Reason = "operator_fired"
	ReasonParentGone    Reason = "parent_gone"
)

type Item struct {
	ID              string                `json:"id"`
	MissionID       string                `json:"missionId"`
	AgentName       string                `json:"agentName,omitempty"`
	Intent          string                `json:"intent,omitempty"`
	ParentSessionID string                `json:"parentSessionId,omitempty"`
	Reason          Reason                `json:"reason"`
	Report          missionservice.Report `json:"report"`
	CreatedAt       time.Time             `json:"createdAt"`
}

type Service interface {
	Add(ctx context.Context, item *Item) error

	List(ctx context.Context, limit int) ([]*Item, error)
}

type service struct {
	db libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{db: db}
}

func (s *service) store() runtimetypes.Store {
	return runtimetypes.New(s.db.WithoutTransaction())
}

func (s *service) Add(ctx context.Context, item *Item) error {
	if item == nil {
		return fmt.Errorf("item is required")
	}
	if item.MissionID == "" {
		return fmt.Errorf("missionId is required")
	}
	if err := validateReason(item.Reason); err != nil {
		return err
	}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal inbox item: %w", err)
	}
	return s.store().SetKV(ctx, inboxKVPrefix+item.ID, raw)
}

func (s *service) List(ctx context.Context, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 100
	}
	kvs, err := s.store().ListKVPrefix(ctx, inboxKVPrefix, nil, limit)
	if err != nil {
		return nil, err
	}
	items := make([]*Item, 0, len(kvs))
	for _, kv := range kvs {
		var it Item
		if err := json.Unmarshal(kv.Value, &it); err != nil {
			return nil, fmt.Errorf("inbox item %q: %w", kv.Key, err)
		}
		items = append(items, &it)
	}
	return items, nil
}

func validateReason(r Reason) error {
	switch r {
	case ReasonOperatorFired, ReasonParentGone:
		return nil
	default:
		return fmt.Errorf("invalid inbox reason %q: must be one of operator_fired|parent_gone", r)
	}
}
