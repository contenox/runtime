package libbus_test

import (
	"context"
	"testing"
	"time"

	"github.com/js402/CATE/libs/libbus"
	"github.com/stretchr/testify/require"
)

func TestPublishAndPop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	subject := "test.publish.pop"
	message := []byte("hello world")

	// Start the Pop call in a goroutine so that the subscription is active before we publish.
	resultCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := ps.Pop(ctx, subject)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- data
	}()

	// Give a brief moment for the Pop subscription to be ready.
	time.Sleep(100 * time.Millisecond)

	// Publish the message.
	err := ps.Publish(ctx, subject, message)
	require.NoError(t, err)

	// Wait for the message.
	select {
	case received := <-resultCh:
		require.Equal(t, message, received)
	case err := <-errCh:
		t.Fatalf("error receiving message: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for message")
	}
}

func TestStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	subject := "test.stream"
	message := []byte("streamed message")

	// Create a channel for streaming messages.
	streamCh := make(chan []byte, 1)
	sub, err := ps.Stream(ctx, subject, streamCh)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Publish the message.
	err = ps.Publish(ctx, subject, message)
	require.NoError(t, err)

	// Wait for the streamed message.
	select {
	case received := <-streamCh:
		require.Equal(t, message, received)
	case <-ctx.Done():
		t.Fatal("timed out waiting for streamed message")
	}
}

func TestQueuePublishAndQueuePop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	subject := "testqueue"
	queue := "worker-group"
	message := []byte("queued message")

	// Start the QueuePop call in a goroutine so that the subscriber is ready.
	resultCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := ps.QueuePop(ctx, subject, queue)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- data
	}()

	// Give a brief moment for the QueuePop subscription to be active.
	time.Sleep(100 * time.Millisecond)

	// Publish the message.
	err := ps.QueuePublish(ctx, subject, message, "msg-123")
	require.NoError(t, err)

	// Wait for the message.
	select {
	case received := <-resultCh:
		require.Equal(t, message, received)
	case err := <-errCh:
		t.Fatalf("error receiving queued message: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for queued message")
	}
}

func TestPublishWithClosedConnection(t *testing.T) {
	ctx := context.Background()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	// Close the connection.
	err := ps.Close()
	require.NoError(t, err)

	// Attempt to publish after closing.
	err = ps.Publish(ctx, "test.closed", []byte("data"))
	require.Error(t, err)
	require.Equal(t, libbus.ErrConnectionClosed, err)
}

func TestPopCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	subject := "test.pop.cancel"

	// Start Pop and expect it to time out.
	_, err := ps.Pop(ctx, subject)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestQueuePopCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ps, cleanup := libbus.NewTestPubSub(t)
	defer cleanup()

	subject := "testqueuecancel"
	queue := "testgroup"

	// Start QueuePop and expect it to time out.
	_, err := ps.QueuePop(ctx, subject, queue)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
