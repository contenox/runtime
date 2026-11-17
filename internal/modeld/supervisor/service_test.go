package supervisor_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/modeld/supervisor"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"version": "test", "protocol": transport.ProtocolVersion, "backends": []string{"llama"}}); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "worker":
			flags := flag.NewFlagSet("worker", flag.ExitOnError)
			cfg := owner.ChildConfig{Tracker: libtracker.NoopTracker{}, Parent: os.Stdin}
			flags.StringVar(&cfg.DataRoot, "data-root", "", "")
			flags.StringVar(&cfg.ModelsDir, "models-dir", "", "")
			flags.StringVar(&cfg.Backend, "backend", "", "")
			flags.StringVar(&cfg.Instance, "worker-instance", "", "")
			flags.StringVar(&cfg.ReadyFile, "ready-file", "", "")
			flags.StringVar(&cfg.OwnerLease, "owner-lease", "", "")
			idleTTL := flags.String("idle-ttl", "", "")
			for _, name := range []string{"mem-max", "mem-reserve", "mem-cold", "mem-headroom", "compute-reserve", "min-hot-context"} {
				flags.String(name, "", "")
			}
			if err := flags.Parse(os.Args[2:]); err != nil {
				os.Exit(1)
			}
			if *idleTTL != "" && *idleTTL != "off" && *idleTTL != "0" {
				value, err := time.ParseDuration(*idleTTL)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				cfg.IdleTTL = value
			}
			err := owner.ServeChild(context.Background(), cfg, cancellableMemory{transport.NewMemoryService(transport.WithOwnerFence(cfg.Instance))})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type ownerFixture struct {
	pool    *modeldconn.Workers
	holder  *owner.Owner
	address string
	root    string
}

func sharedOwner(t *testing.T) ownerFixture {
	t.Helper()
	root := t.TempDir()
	binary, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("CONTENOX_MODELD_BIN", binary)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "models", "fixture"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "models", "fixture", "model.gguf"), []byte("in-memory-backend-fixture"), 0o600))
	for _, name := range []string{"model", "old", "new"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "models", name), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "models", name, "model.gguf"), []byte("admission-fixture-"+name), 0o600))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	holder, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: 5 * time.Second, Endpoint: listener.Addr().String(), Backend: "shared", Tracker: libtracker.NoopTracker{}})
	require.NoError(t, err)
	require.True(t, holder.IsOwner())
	t.Cleanup(func() { require.NoError(t, holder.Release()) })
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "scheduler.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	scheduler, err := admission.NewEventScheduler(ctx, db, bus, admission.DefaultConfig(), func(err error) { t.Error(err) })
	require.NoError(t, err)
	pool, err := modeldconn.NewOwnerWorkers(ctx, root, holder.InstanceID(), scheduler)
	require.NoError(t, err)
	service, err := supervisor.New(pool, filepath.Join(root, "models"), holder.InstanceID(), func(context.Context) (transport.Inventory, error) { return transport.Inventory{}, nil })
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, holder.InstanceID(), "shared") }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done); _ = pool.Close() })
	return ownerFixture{pool: pool, holder: holder, address: listener.Addr().String(), root: root}
}

func connectOwner(t *testing.T, fixture ownerFixture) *transportgrpc.Client {
	t.Helper()
	client, err := transportgrpc.DialLeader(fixture.address, fixture.holder.InstanceID())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func roleRequest(fixture ownerFixture, role string) transport.OpenSessionRequest {
	return transport.OpenSessionRequest{
		Fence: transport.Fence{OwnerInstanceID: fixture.holder.InstanceID()}, ModelName: "fixture", Type: "llama",
		Config: transport.Config{NumCtx: 256, SessionNamespace: "same-session-name", Execution: &transport.Execution{Role: role, Priority: "foreground", RequesterID: "same-editor"}},
	}
}

func TestUnit_SharedOwner_ClientDisconnectReleasesOnlyItsSessionAndReusesWorker(t *testing.T) {
	fixture := sharedOwner(t)
	first, second := connectOwner(t, fixture), connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := first.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	_, err = session.EnsurePrefix(ctx, transport.PrefixInput{Text: "private to the first client"})
	require.NoError(t, err)
	type result struct {
		session transport.Session
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		sess, err := second.OpenSession(ctx, roleRequest(fixture, "alternative"))
		opened <- result{sess, err}
	}()
	require.Eventually(t, func() bool {
		status, err := second.Admission(ctx)
		return err == nil && len(status.Workers) == 1 && status.Workers[0].QueuedForeground == 1
	}, 5*time.Second, 25*time.Millisecond)
	require.NoError(t, first.Close())
	next := <-opened
	require.NoError(t, next.err)
	require.Zero(t, next.session.ExplainContext().ResidentTokens)
	status, err := second.Status(ctx)
	require.NoError(t, err)
	require.Len(t, status.Workers, 1)
	require.Equal(t, fixture.holder.InstanceID(), status.OwnerInstanceID)
	unload := transport.UnloadModelRequest{Fence: transport.Fence{OwnerInstanceID: fixture.holder.InstanceID()}, WorkerInstance: status.Workers[0].Instance}
	require.ErrorIs(t, second.UnloadModel(ctx, unload), transport.ErrModelBusy)
	require.NoError(t, next.session.Close())
	require.NoError(t, second.UnloadModel(ctx, unload))
	require.NoError(t, second.Close())
	probe := connectOwner(t, fixture)
	_, err = probe.Health(ctx)
	require.NoError(t, err)
	require.Empty(t, fixture.pool.Snapshot())
}

func TestUnit_SharedOwner_ChildFencesItselfWhenOwnerLeaseDisappears(t *testing.T) {
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	require.NoError(t, fixture.holder.Release())
	require.NoError(t, modeldconn.WaitForPreviousWorkers(ctx, fixture.root, "successor"))
	_, err = session.EnsurePrefix(ctx, transport.PrefixInput{Text: "stale request"})
	require.Error(t, err)
	_, err = client.OpenSession(ctx, transport.OpenSessionRequest{Fence: transport.Fence{OwnerInstanceID: "stale"}, ModelName: "fixture", Type: "llama"})
	require.ErrorIs(t, err, transport.ErrStaleFence)
}

func TestUnit_SharedOwner_ChildrenKeepTheirReviewedConfiguration(t *testing.T) {
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(fixture.root, "modeld.json")
	original := `{"memory":{"max_resident":"2GiB","reserve_free":"1GiB"},"idle_ttl":"off"}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	first, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	require.NoError(t, first.Close())
	before := fixture.pool.Snapshot()
	require.Len(t, before, 1)
	require.NotEmpty(t, before[0].Spec.ConfigurationDigest)
	files, err := filepath.Glob(filepath.Join(fixture.root, "workers", fixture.holder.InstanceID(), "*", "modeld.json"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	snapshot, err := os.ReadFile(files[0])
	require.NoError(t, err)
	require.JSONEq(t, original, string(snapshot))
	equivalent := `{"idle_ttl": "off", "memory": {"reserve_free": "1GiB", "max_resident": "2GiB"}}`
	require.NoError(t, os.WriteFile(path, []byte(equivalent), 0o600))
	second, err := client.OpenSession(ctx, roleRequest(fixture, "alternative"))
	require.NoError(t, err)
	require.NoError(t, second.Close())
	require.Len(t, fixture.pool.Snapshot(), 1)
	require.NoError(t, os.WriteFile(path, []byte(`{"memory":{"max_resident":"3GiB"}}`), 0o600))
	third, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	require.NoError(t, third.Close())
	require.Len(t, fixture.pool.Snapshot(), 2)
	snapshot, err = os.ReadFile(files[0])
	require.NoError(t, err)
	require.JSONEq(t, original, string(snapshot))
	require.NoError(t, os.WriteFile(path, []byte(`{"memory":`), 0o600))
	_, err = client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.ErrorContains(t, err, "modeld.json is invalid")
	require.Len(t, fixture.pool.Snapshot(), 2)
}
