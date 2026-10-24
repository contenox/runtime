package messagestore

import (
	"context"
	"time"
)

type Message struct {
	ID      string    `json:"id"`
	IDX     string    `json:"idx_id"`
	Payload []byte    `json:"payload"`
	AddedAt time.Time `json:"added_at"`
}

type SessionInfo struct {
	ID           string
	Identity     string
	Name         string
	MessageCount int
	UpdatedAt    time.Time
}

type Store interface {
	CreateMessageIndex(ctx context.Context, id string, identity string) error
	CreateNamedMessageIndex(ctx context.Context, id string, identity string, name string) error
	DeleteMessageIndex(ctx context.Context, id string, identity string) error
	ListMessageStreams(ctx context.Context, identity string) ([]string, error)
	ListMessageIndices(ctx context.Context, identity string) ([]string, error)
	ListAllSessions(ctx context.Context, identity string) ([]SessionInfo, error)
	GetSessionByName(ctx context.Context, identity string, name string) (*SessionInfo, error)
	RenameSession(ctx context.Context, id string, name string) error

	AppendMessages(ctx context.Context, messages ...*Message) error
	DeleteMessages(ctx context.Context, stream string) error
	ListMessages(ctx context.Context, stream string) ([]*Message, error)
	LastMessage(ctx context.Context, stream string) (*Message, error)
	CountMessages(ctx context.Context, stream string) (int, error)
}
