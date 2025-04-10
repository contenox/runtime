package libbus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

var (
	ErrConnectionClosed       = errors.New("connection closed")
	ErrSubscriptionFailed     = errors.New("subscription failed")
	ErrStreamCreationFailed   = errors.New("stream creation failed")
	ErrStreamSubscriptionFail = errors.New("stream subscription failed")
	ErrMessageAcknowledge     = errors.New("failed to acknowledge message")
	ErrMessagePublish         = errors.New("message publishing failed")
	ErrQueueSubscriptionFail  = errors.New("queue subscription failed")
)

// Real-time event notifications (e.g., job state updates to a UI)
// Triggering ephemeral tasks (e.g., quick, non-persistent jobs)
// Distributing lightweight messages between services
type Messenger interface {
	// Publish sends a message on the given subject.
	Publish(ctx context.Context, subject string, data []byte) error

	// QueuePublish sends a message to a subject in a queue group. If a msgID is provided, it uses JetStream’s deduplication.
	QueuePublish(ctx context.Context, subject string, data []byte, msgID string) error

	// Pop blocks until a message is available on the given subject, or the context is canceled.
	Pop(ctx context.Context, subject string) ([]byte, error)

	// QueuePop blocks until a message is available from the specified queue.
	QueuePop(ctx context.Context, subject, queue string) ([]byte, error)

	// Stream streams messages (using channels) from the given subject.
	Stream(ctx context.Context, subject string, ch chan<- []byte) (Subscription, error)

	// Close cleans up any underlying resources.
	Close() error
}

type Subscription interface {
	Unsubscribe() error
}

type ps struct {
	nc *nats.Conn
}

type natsSubscription struct {
	sub *nats.Subscription
}

var Pubsub = &ps{} // Instance for direct access if needed

type Config struct {
	NATSURL      string
	NATSPassword string
	NATSUser     string
}

func NewPubSub(ctx context.Context, cfg *Config) (Messenger, error) {
	var nc *nats.Conn
	var err error
	if cfg.NATSUser == "" {
		nc, err = nats.Connect(
			cfg.NATSURL,
			nats.ClosedHandler(func(_ *nats.Conn) {
				// TODO: Handle connection closed events
			}),
		)
	} else {
		if cfg.NATSUser == "" {
			nc, err = nats.Connect(
				cfg.NATSURL,
				nats.UserInfo(cfg.NATSUser, cfg.NATSPassword),
				nats.ClosedHandler(func(_ *nats.Conn) {
					// TODO: Handle connection closed events
				}),
			)
		}
	}
	if err != nil {
		return nil, err
	}

	return &ps{nc: nc}, nil
}

// Pop waits for a message on the specified subject and returns its data.
func (p *ps) Pop(ctx context.Context, subject string) ([]byte, error) {
	if p.nc == nil || p.nc.IsClosed() {
		return nil, ErrConnectionClosed
	}

	sub, err := p.nc.SubscribeSync(subject)
	if err != nil {
		return nil, fmt.Errorf("%w: could not subscribe to subject %s", ErrSubscriptionFailed, subject)
	}
	defer sub.Unsubscribe()

	msg, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve message from %s: %w", subject, err)
	}
	return msg.Data, nil
}

// QueuePop waits for a message on the specified subject and queue, then returns its data.
// This uses JetStream to enforce exactly-once semantics by acknowledging the message.
func (p *ps) QueuePop(ctx context.Context, subject, queue string) ([]byte, error) {
	// Ensure JetStream is initialized
	js, err := p.nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		return nil, err
	}

	// Check if the stream exists
	_, err = js.StreamInfo(subject)
	if err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) {
			// Create stream if not found
			_, err = js.AddStream(&nats.StreamConfig{
				Name:      subject,
				Subjects:  []string{subject},
				Retention: nats.WorkQueuePolicy,
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Queue Subscription
	sub, err := js.QueueSubscribeSync(subject, queue)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()

	// Wait for message
	msg, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, err
	}

	// Acknowledge the message
	msg.Ack()

	return msg.Data, nil
}

func (p *ps) Publish(ctx context.Context, subject string, data []byte) error {
	if p.nc == nil || p.nc.IsClosed() {
		return ErrConnectionClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		err := p.nc.Publish(subject, data)
		if err != nil {
			return fmt.Errorf("%w: failed to publish to %s", ErrMessagePublish, subject)
		}
		return nil
	}
}

// QueuePublish publishes a message to a subject that is expected to be consumed
// by subscribers in a queue group. If a msgID is provided, it uses JetStream's
// deduplication mechanism to help enforce exactly-once semantics.
func (p *ps) QueuePublish(ctx context.Context, subject string, data []byte, msgID string) error {
	if p.nc == nil || p.nc.IsClosed() {
		return ErrConnectionClosed
	}

	js, err := p.nc.JetStream()
	if err != nil {
		return err
	}

	// Ensure stream exists; create it if not found.
	_, err = js.StreamInfo(subject)
	if err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) {
			_, err = js.AddStream(&nats.StreamConfig{
				Name:      subject,
				Subjects:  []string{subject},
				Retention: nats.WorkQueuePolicy,
			})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}
	if msgID != "" {
		msg.Header.Set("Nats-Msg-Id", msgID)
	}

	// Use synchronous publish to ensure the message is stored in JetStream.
	_, err = js.PublishMsg(msg)
	return err
}

func (p *ps) Stream(ctx context.Context, subject string, ch chan<- []byte) (Subscription, error) {
	return p.stream(ctx, subject, "", ch)
}

func (p *ps) stream(ctx context.Context, subject, queue string, ch chan<- []byte) (Subscription, error) {
	if p.nc == nil || p.nc.IsClosed() {
		return nil, ErrConnectionClosed
	}

	natsChan := make(chan *nats.Msg, 1024)
	var sub *nats.Subscription
	var err error

	if queue == "" {
		sub, err = p.nc.ChanSubscribe(subject, natsChan)
	} else {
		sub, err = p.nc.ChanQueueSubscribe(subject, queue, natsChan)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: unable to subscribe to stream %s", ErrStreamSubscriptionFail, subject)
	}

	go func() {
		defer sub.Unsubscribe()
		defer close(natsChan)

		for {
			select {
			case msg, ok := <-natsChan:
				if !ok {
					return
				}
				select {
				case ch <- msg.Data:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return &natsSubscription{sub: sub}, nil
}

func (p *ps) Close() error {
	if p.nc != nil && !p.nc.IsClosed() {
		p.nc.Close()
	}
	return nil
}

func (s *natsSubscription) Unsubscribe() error {
	return s.sub.Unsubscribe()
}

// NewTestPubSub starts a NATS container using SetupNatsInstance,
// creates a new PubSub instance, and returns it along with a cleanup function.
func NewTestPubSub(t *testing.T) (Messenger, func()) {
	ctx := context.Background()
	cons, container, cleanup, err := SetupNatsInstance(ctx)
	require.NoError(t, err)
	// Optionally log container status if needed.
	log.Printf("NATS container running: %v", container)

	cfg := &Config{
		NATSURL: cons,
	}
	ps, err := NewPubSub(ctx, cfg)
	require.NoError(t, err)

	// Return a cleanup function that closes PubSub and terminates the container.
	return ps, func() {
		_ = ps.Close()
		cleanup()
	}
}
