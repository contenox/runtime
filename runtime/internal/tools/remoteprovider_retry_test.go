package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime/libbus"
)

type flakyMessenger struct {
	failures int
	calls    int
	reply    []byte
}

func (f *flakyMessenger) Publish(context.Context, string, []byte) error { return nil }
func (f *flakyMessenger) Stream(context.Context, string, chan<- []byte) (libbus.Subscription, error) {
	return nil, nil
}
func (f *flakyMessenger) Request(context.Context, string, []byte) ([]byte, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, libbus.ErrNoResponders
	}
	return f.reply, nil
}
func (f *flakyMessenger) Serve(context.Context, string, libbus.Handler) (libbus.Subscription, error) {
	return nil, nil
}
func (f *flakyMessenger) Close() error { return nil }

func TestUnit_RequestMCPRetriesUntilAWorkerSubscribes(t *testing.T) {
	messenger := &flakyMessenger{failures: 2, reply: []byte("ok")}
	reply, err := requestMCP(context.Background(), messenger, "subject", []byte("payload"))
	if err != nil {
		t.Fatalf("requestMCP: %v", err)
	}
	if string(reply) != "ok" {
		t.Fatalf("reply = %q, want ok", reply)
	}
	if messenger.calls != 3 {
		t.Fatalf("calls = %d, want 3", messenger.calls)
	}
}

func TestUnit_RequestMCPDoesNotRetryOtherErrors(t *testing.T) {
	messenger := &failingMessenger{err: errors.New("decode failed")}
	if _, err := requestMCP(context.Background(), messenger, "subject", nil); err == nil {
		t.Fatal("expected the error to be returned")
	}
	if messenger.calls != 1 {
		t.Fatalf("calls = %d, want 1", messenger.calls)
	}
}

type failingMessenger struct {
	err   error
	calls int
}

func (f *failingMessenger) Publish(context.Context, string, []byte) error { return nil }
func (f *failingMessenger) Stream(context.Context, string, chan<- []byte) (libbus.Subscription, error) {
	return nil, nil
}
func (f *failingMessenger) Request(context.Context, string, []byte) ([]byte, error) {
	f.calls++
	return nil, f.err
}
func (f *failingMessenger) Serve(context.Context, string, libbus.Handler) (libbus.Subscription, error) {
	return nil, nil
}
func (f *failingMessenger) Close() error { return nil }
