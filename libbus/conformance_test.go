package libbus_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type newBusFunc func(t *testing.T) libbus.Messenger

type backendProps struct {
	waitsForLateHandler bool
	drainsOnUnsubscribe bool
}

type backend struct {
	name   string
	newBus newBusFunc
	props  backendProps
}

func conformanceBackends() []backend {
	return []backend{
		{
			name: "InMem",
			newBus: func(t *testing.T) libbus.Messenger {
				t.Helper()
				b := libbus.NewInMem()
				t.Cleanup(func() { _ = b.Close() })
				return b
			},
		},
		{
			name: "SQLite",
			newBus: func(t *testing.T) libbus.Messenger {
				t.Helper()
				return newTestBus(t)
			},
			props: backendProps{waitsForLateHandler: true, drainsOnUnsubscribe: true},
		},
		{
			name:   "NATS",
			newBus: newNATSBus,
		},
	}
}

func runConformance(t *testing.T, name string, fn func(t *testing.T, newBus newBusFunc)) {
	t.Helper()
	for _, be := range conformanceBackends() {
		t.Run(name+"/"+be.name, func(t *testing.T) {
			fn(t, be.newBus)
		})
	}
}

var subjectCounter atomic.Uint64

func uniqueSubject(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("conformance.s%d", subjectCounter.Add(1))
}

var (
	natsOnce    sync.Once
	natsURL     string
	natsErr     error
	natsCleanup = func() {}
)

func newNATSBus(t *testing.T) libbus.Messenger {
	t.Helper()
	natsOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		url, _, cleanup, err := libbus.SetupNatsInstance(ctx)
		natsURL, natsErr, natsCleanup = url, err, cleanup
	})
	if natsErr != nil {
		msg := fmt.Sprintf("NATS backend NOT exercised: could not start the nats:2.10 container (%v). "+
			"Docker is required to run the NATS half of the conformance suite.", natsErr)
		if os.Getenv("LIBBUS_REQUIRE_NATS") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	bus, err := libbus.NewPubSub(context.Background(), &libbus.Config{NATSURL: natsURL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

func TestMain(m *testing.M) {
	code := m.Run()
	natsCleanup()
	os.Exit(code)
}

func receive(t *testing.T, ch <-chan []byte, within time.Duration) []byte {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(within):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

func expectNoMessage(t *testing.T, ch <-chan []byte, within time.Duration) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected message: %q", string(msg))
	case <-time.After(within):
	}
}

const settle = 250 * time.Millisecond

func TestConformance_PublishStreamDelivery(t *testing.T) {
	runConformance(t, "publish_is_delivered_to_subscriber", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		ch := make(chan []byte, 4)
		sub, err := bus.Stream(ctx, subject, ch)
		require.NoError(t, err)
		defer sub.Unsubscribe()

		require.NoError(t, bus.Publish(ctx, subject, []byte("hello")))
		assert.Equal(t, "hello", string(receive(t, ch, 5*time.Second)))
	})
}

func TestConformance_PublishPreservesOrder(t *testing.T) {
	runConformance(t, "messages_arrive_in_publish_order", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		ch := make(chan []byte, 64)
		sub, err := bus.Stream(ctx, subject, ch)
		require.NoError(t, err)
		defer sub.Unsubscribe()

		const n = 20
		for i := range n {
			require.NoError(t, bus.Publish(ctx, subject, fmt.Appendf(nil, "msg-%02d", i)))
		}

		got := make([]string, 0, n)
		for range n {
			got = append(got, string(receive(t, ch, 5*time.Second)))
		}
		want := make([]string, 0, n)
		for i := range n {
			want = append(want, fmt.Sprintf("msg-%02d", i))
		}
		assert.Equal(t, want, got)
	})
}

func TestConformance_MultipleSubscribers(t *testing.T) {
	runConformance(t, "every_subscriber_gets_every_message", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		chA := make(chan []byte, 4)
		chB := make(chan []byte, 4)
		subA, err := bus.Stream(ctx, subject, chA)
		require.NoError(t, err)
		defer subA.Unsubscribe()
		subB, err := bus.Stream(ctx, subject, chB)
		require.NoError(t, err)
		defer subB.Unsubscribe()

		require.NoError(t, bus.Publish(ctx, subject, []byte("fanout")))

		assert.Equal(t, "fanout", string(receive(t, chA, 5*time.Second)))
		assert.Equal(t, "fanout", string(receive(t, chB, 5*time.Second)))
	})
}

func TestConformance_SubjectIsolation(t *testing.T) {
	runConformance(t, "subscriber_does_not_see_other_subjects", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		mine, theirs := uniqueSubject(t), uniqueSubject(t)

		ch := make(chan []byte, 4)
		sub, err := bus.Stream(ctx, mine, ch)
		require.NoError(t, err)
		defer sub.Unsubscribe()

		require.NoError(t, bus.Publish(ctx, theirs, []byte("not for you")))
		expectNoMessage(t, ch, settle)
	})
}

func TestConformance_PublishWithNoSubscribers(t *testing.T) {
	runConformance(t, "publish_without_subscribers_succeeds", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		require.NoError(t, bus.Publish(ctx, uniqueSubject(t), []byte("into the void")))
	})
}

func TestConformance_UnsubscribeStopsDelivery(t *testing.T) {
	runConformance(t, "no_delivery_after_unsubscribe", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		ch := make(chan []byte, 8)
		sub, err := bus.Stream(ctx, subject, ch)
		require.NoError(t, err)

		require.NoError(t, bus.Publish(ctx, subject, []byte("before")))
		assert.Equal(t, "before", string(receive(t, ch, 5*time.Second)))

		require.NoError(t, sub.Unsubscribe())
		require.NoError(t, bus.Publish(ctx, subject, []byte("after")))
		expectNoMessage(t, ch, settle)
	})
}

func TestConformance_StreamContextCancelStopsDelivery(t *testing.T) {
	runConformance(t, "cancelling_stream_context_stops_delivery", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		subject := uniqueSubject(t)

		streamCtx, cancel := context.WithCancel(t.Context())
		ch := make(chan []byte, 8)
		sub, err := bus.Stream(streamCtx, subject, ch)
		require.NoError(t, err)
		defer sub.Unsubscribe()

		require.NoError(t, bus.Publish(t.Context(), subject, []byte("before")))
		assert.Equal(t, "before", string(receive(t, ch, 5*time.Second)))

		cancel()
		time.Sleep(settle)

		require.NoError(t, bus.Publish(t.Context(), subject, []byte("after")))
		expectNoMessage(t, ch, settle)
	})
}

func TestConformance_StreamWithCancelledContext(t *testing.T) {
	runConformance(t, "stream_with_cancelled_context_errors", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := bus.Stream(ctx, uniqueSubject(t), make(chan []byte, 1))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestConformance_PublishWithCancelledContext(t *testing.T) {
	runConformance(t, "publish_with_cancelled_context_errors", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.Error(t, bus.Publish(ctx, uniqueSubject(t), []byte("data")))
	})
}

func TestConformance_PublishDoesNotBlockOnStuckConsumer(t *testing.T) {
	runConformance(t, "publish_never_blocks_on_a_stuck_consumer", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		stuck := make(chan []byte)
		sub, err := bus.Stream(ctx, subject, stuck)
		require.NoError(t, err)
		defer sub.Unsubscribe()

		done := make(chan error, 1)
		go func() {
			for range 20 {
				if err := bus.Publish(ctx, subject, []byte("backpressure")); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()

		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Publish blocked on a stuck consumer — a slow subscriber must not stall the publisher")
		}
	})
}

func TestConformance_RequestReply(t *testing.T) {
	runConformance(t, "request_reaches_handler_and_returns_reply", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		sub, err := bus.Serve(ctx, subject, func(_ context.Context, data []byte) ([]byte, error) {
			return append([]byte("echo:"), data...), nil
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		reply, err := bus.Request(reqCtx, subject, []byte("ping"))
		require.NoError(t, err)
		assert.Equal(t, "echo:ping", string(reply))
	})
}

func TestConformance_RequestWithNoHandler(t *testing.T) {
	runConformance(t, "request_without_handler_times_out", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		_, err := bus.Request(ctx, uniqueSubject(t), []byte("anyone there?"))
		require.ErrorIs(t, err, libbus.ErrRequestTimeout)
	})
}

func TestConformance_RequestHandlerErrorBecomesReply(t *testing.T) {
	runConformance(t, "handler_error_is_a_reply_not_a_transport_error", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		sub, err := bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, fmt.Errorf("handler exploded")
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		reply, err := bus.Request(reqCtx, subject, []byte("boom"))
		require.NoError(t, err)
		assert.Equal(t, "error: handler exploded", string(reply))
	})
}

func TestConformance_RequestWithCancelledContext(t *testing.T) {
	runConformance(t, "request_with_cancelled_context_reports_cancellation", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := bus.Request(ctx, uniqueSubject(t), []byte("data"))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestConformance_RequestInFlightCancellation(t *testing.T) {
	runConformance(t, "cancelling_an_inflight_request_unblocks_the_caller", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		subject := uniqueSubject(t)

		sub, err := bus.Serve(t.Context(), subject, func(ctx context.Context, _ []byte) ([]byte, error) {
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
			}
			return []byte("late"), nil
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		reqCtx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() {
			_, err := bus.Request(reqCtx, subject, []byte("data"))
			errCh <- err
		}()

		time.Sleep(100 * time.Millisecond)
		cancel()

		select {
		case err := <-errCh:
			require.Error(t, err, "a cancelled request must not report success")
		case <-time.After(10 * time.Second):
			t.Fatal("Request did not return after its context was cancelled")
		}
	})
}

func TestConformance_ServeContextCancelStopsHandling(t *testing.T) {
	runConformance(t, "cancelling_serve_context_stops_handling", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		subject := uniqueSubject(t)

		serveCtx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int64
		_, err := bus.Serve(serveCtx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			calls.Add(1)
			return []byte("pong"), nil
		})
		require.NoError(t, err)

		cancel()
		time.Sleep(settle)

		reqCtx, reqCancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer reqCancel()
		_, err = bus.Request(reqCtx, subject, []byte("ping"))
		require.Error(t, err, "a request must not be served after the Serve context was cancelled")
		assert.Zero(t, calls.Load(), "handler ran after its context was cancelled")
	})
}

func TestConformance_ServeUnsubscribeStopsHandling(t *testing.T) {
	runConformance(t, "unsubscribing_a_handler_stops_handling", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		sub, err := bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte("pong"), nil
		})
		require.NoError(t, err)

		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		reply, err := bus.Request(reqCtx, subject, []byte("ping"))
		cancel()
		require.NoError(t, err)
		require.Equal(t, "pong", string(reply))

		require.NoError(t, sub.Unsubscribe())
		time.Sleep(settle)

		reqCtx2, cancel2 := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel2()
		_, err = bus.Request(reqCtx2, subject, []byte("ping"))
		require.Error(t, err, "handler still answered after Unsubscribe")
	})
}

func TestConformance_SequentialRequests(t *testing.T) {
	runConformance(t, "handler_serves_repeated_requests", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		var served atomic.Int64
		sub, err := bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return fmt.Appendf(nil, "%d", served.Add(1)), nil
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		for i := 1; i <= 5; i++ {
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			reply, err := bus.Request(reqCtx, subject, nil)
			cancel()
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%d", i), string(reply))
		}
	})
}

func TestConformance_CloseSemantics(t *testing.T) {
	runConformance(t, "operations_after_close_report_connection_closed", func(t *testing.T, newBus newBusFunc) {
		ctx := context.Background()
		bus := newBus(t)
		subject := uniqueSubject(t)

		_, err := bus.Stream(ctx, subject, make(chan []byte, 1)) //nolint:errcheck // subscription is torn down by Close
		require.NoError(t, err)
		_, err = bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		require.NoError(t, err)

		closed := make(chan error, 1)
		go func() { closed <- bus.Close() }()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("Close blocked with live subscriptions outstanding")
		}

		require.ErrorIs(t, bus.Publish(ctx, subject, []byte("data")), libbus.ErrConnectionClosed)

		_, err = bus.Stream(ctx, subject, make(chan []byte, 1))
		require.ErrorIs(t, err, libbus.ErrConnectionClosed)

		reqCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, err = bus.Request(reqCtx, subject, []byte("data"))
		require.ErrorIs(t, err, libbus.ErrConnectionClosed)
	})
}

func TestConformance_CloseIsIdempotent(t *testing.T) {
	runConformance(t, "close_twice_is_safe", func(t *testing.T, newBus newBusFunc) {
		bus := newBus(t)
		require.NoError(t, bus.Close())
		require.NoError(t, bus.Close())
	})
}

func TestConformanceDivergence_LateHandler(t *testing.T) {
	for _, be := range conformanceBackends() {
		t.Run(be.name, func(t *testing.T) {
			bus := be.newBus(t)
			subject := uniqueSubject(t)

			go func() {
				time.Sleep(200 * time.Millisecond)
				_, _ = bus.Serve(context.Background(), subject, func(_ context.Context, _ []byte) ([]byte, error) {
					return []byte("pong"), nil
				})
			}()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			reply, err := bus.Request(ctx, subject, []byte("ping"))

			if be.props.waitsForLateHandler {
				require.NoError(t, err, "%s is documented to wait for a late handler", be.name)
				assert.Equal(t, "pong", string(reply))
				return
			}
			require.ErrorIs(t, err, libbus.ErrRequestTimeout,
				"%s is documented to fail fast when no handler is registered yet", be.name)
			require.ErrorIs(t, err, libbus.ErrNoResponders,
				"%s must report a missing handler as ErrNoResponders, which is what lets a caller retry the request without risking a second execution", be.name)
		})
	}
}

func TestConformanceDivergence_UnsubscribeDrain(t *testing.T) {
	for _, be := range conformanceBackends() {
		t.Run(be.name, func(t *testing.T) {
			if !be.props.drainsOnUnsubscribe {
				t.Skipf("%s does not promise to drain buffered events on Unsubscribe", be.name)
			}
			ctx := t.Context()
			bus := be.newBus(t)
			subject := uniqueSubject(t)

			ch := make(chan []byte, 4)
			sub, err := bus.Stream(ctx, subject, ch)
			require.NoError(t, err)

			require.NoError(t, bus.Publish(ctx, subject, []byte("last-event")))
			require.NoError(t, sub.Unsubscribe())

			select {
			case msg := <-ch:
				assert.Equal(t, "last-event", string(msg))
			default:
				t.Fatal("drain-on-unsubscribe lost the pending event")
			}
		})
	}
}

func TestConformance_TwoHandlersOnOneSubjectBothSurvive(t *testing.T) {
	runConformance(t, "second_handler_joins_rather_than_replaces", func(t *testing.T, newBus newBusFunc) {
		ctx := t.Context()
		bus := newBus(t)
		subject := uniqueSubject(t)

		asked := func(bus libbus.Messenger) error {
			reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := bus.Request(reqCtx, subject, nil)
			return err
		}
		served := func(bus libbus.Messenger) bool {
			if err := asked(bus); err == nil {
				return true
			}
			require.Eventually(t, func() bool { return asked(bus) == nil }, 3*time.Second, 50*time.Millisecond)
			return false
		}

		first, err := bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte("first"), nil
		})
		require.NoError(t, err)
		second, err := bus.Serve(ctx, subject, func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte("second"), nil
		})
		require.NoError(t, err)

		require.True(t, served(bus), "a subject with two handlers must still answer")

		require.NoError(t, first.Unsubscribe())
		require.True(t, served(bus), "unsubscribing the shadowed handler must not silence the one serving")
		require.NoError(t, second.Unsubscribe())
	})
}
