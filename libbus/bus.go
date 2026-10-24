package libbus

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrConnectionClosed       = errors.New("connection closed")
	ErrStreamSubscriptionFail = errors.New("stream subscription failed")
	ErrMessagePublish         = errors.New("message publishing failed")
	ErrRequestTimeout         = errors.New("request timed out")
	ErrNoResponders           = fmt.Errorf("%w: no handler was subscribed to the subject, so the request was never delivered and is safe to retry", ErrRequestTimeout)
)

type Handler func(ctx context.Context, data []byte) ([]byte, error)

type Messenger interface {
	Publish(ctx context.Context, subject string, data []byte) error

	Stream(ctx context.Context, subject string, ch chan<- []byte) (Subscription, error)

	Request(ctx context.Context, subject string, data []byte) ([]byte, error)

	Serve(ctx context.Context, subject string, handler Handler) (Subscription, error)

	Close() error
}

type Subscription interface {
	Unsubscribe() error
}
