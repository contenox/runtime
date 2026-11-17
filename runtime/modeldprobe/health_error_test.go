package modeldprobe_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/liblease"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"google.golang.org/grpc"
)

func TestUnit_ProbePreservesOwnerFailureAndRepair(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol int
		ready    bool
		instance string
		want     string
	}{
		{name: "protocol", protocol: transport.ProtocolVersion - 1, ready: true, want: "update and restart"},
		{name: "not ready", protocol: transport.ProtocolVersion, want: "modeld reports not ready"},
		{name: "owner mismatch", protocol: transport.ProtocolVersion, ready: true, instance: "another-owner", want: "owner mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			root := t.TempDir()
			lease, err := liblease.Acquire(filepath.Join(root, "modeld.lease"), time.Minute,
				liblease.WithMeta(map[string]string{"endpoint": listener.Addr().String(), "backend": "shared"}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lease.Release() })
			instance := tc.instance
			if instance == "" {
				instance = lease.InstanceID()
			}
			server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
				var request struct{}
				if err := stream.RecvMsg(&request); err != nil {
					return err
				}
				return stream.SendMsg(map[string]any{"instance_id": instance, "ready": tc.ready, "backend": "shared", "protocol": tc.protocol})
			}))
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			status := modeldprobe.New(root).Probe(ctx)
			if status.State != modeldprobe.StateUnreachable || !errors.Is(status.Err(), modeldprobe.ErrUnreachable) {
				t.Fatalf("status = %s, error = %v", status.State, status.Err())
			}
			if !strings.Contains(status.Err().Error(), tc.want) {
				t.Fatalf("probe lost %q: %v", tc.want, status.Err())
			}
			observed, err := liblease.Inspect(filepath.Join(root, "modeld.lease"))
			if err != nil || observed.InstanceID != lease.InstanceID() {
				t.Fatalf("probe changed owner lease: %v, %v", observed, err)
			}
		})
	}
}
