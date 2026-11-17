package sessionkit_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
)

type reportingBackend struct {
	transport.Service
	execution     transport.SessionExecution
	describeError error
	opens         int
}

func (b *reportingBackend) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{EffectiveContext: 8192, RequiredBytes: 8192}, b.describeError
}

func (b *reportingBackend) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	b.opens++
	session, err := b.Service.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return sessionkit.WithExecution(session, b.execution)
}

func TestUnit_ExecutionReportSurvivesSlotAndTransport(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(model, []byte("test artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := transport.SessionExecution{
		Backend:   "llama",
		Requested: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAutomatic}, SessionNamespace: "private-client", Execution: &transport.Execution{RequesterID: "private-editor"}},
		Resolved:  transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, NumCtx: 2048, TensorSplit: []float32{1, 2}},
		Capacity:  transport.ModelInfo{EffectiveContext: 2048, RequiredBytes: 4096, Devices: []transport.DeviceInfo{{ID: "physical-device", Operations: []string{"chat"}}}},
	}
	backend := &reportingBackend{Service: transport.NewMemoryService(), execution: want}
	service := slot.New(backend, slot.WithDataRoot(root))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- transportgrpc.Serve(ctx, listener, service, "owner", "llama") }()
	client, err := transportgrpc.DialLeader(listener.Addr().String(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
		if err := <-served; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	req := transport.OpenSessionRequest{ModelName: "model", Type: "llama", Path: model, Config: transport.Config{NumCtx: 2048}}
	session, err := client.OpenSession(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	report := session.ExplainContext()
	if report.Closed || report.FatalError != "" || report.Execution == nil {
		t.Fatalf("execution report = %+v", report)
	}
	if report.Execution.Resolved.NumCtx != 2048 || report.Execution.Resolved.Target.Mode != transport.PlacementCPU || report.Execution.Requested.Target.Mode != transport.PlacementAutomatic {
		t.Fatalf("native resolution lost over transport: %+v", report.Execution)
	}
	if report.Execution.Requested.SessionNamespace != "" || report.Execution.Requested.Execution != nil {
		t.Fatalf("report contains client admission metadata: %+v", report.Execution)
	}
	described, err := client.Describe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if described.EffectiveContext != 2048 || described.RequiredBytes != 4096 {
		t.Fatalf("resident model describes a pre-open estimate: %+v", described)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if report := session.ExplainContext(); !report.Closed || report.FatalError == "" {
		t.Fatalf("closed session lost its failure: %+v", report)
	}
}

func TestUnit_ExecutionReportCopiesMutableSettings(t *testing.T) {
	original := transport.SessionExecution{Backend: "llama", Resolved: transport.Config{TensorSplit: []float32{1}}, Capacity: transport.ModelInfo{Devices: []transport.DeviceInfo{{Operations: []string{"chat"}}}}}
	copy := transport.CopyExecutionReport(original)
	copy.Resolved.TensorSplit[0] = 9
	copy.Capacity.Devices[0].Operations[0] = "changed"
	if original.Resolved.TensorSplit[0] != 1 || original.Capacity.Devices[0].Operations[0] != "chat" {
		t.Fatal("execution report shares mutable settings")
	}
}

func TestUnit_SlotReportsDescriptionFailureBeforeOpening(t *testing.T) {
	want := errors.New("requested device is unavailable")
	backend := &reportingBackend{Service: transport.NewMemoryService(), describeError: want}
	service := slot.New(backend)
	_, err := service.OpenSession(t.Context(), transport.OpenSessionRequest{ModelName: "test", Path: "test.gguf"})
	if !errors.Is(err, want) || backend.opens != 0 {
		t.Fatalf("error=%v opens=%d", err, backend.opens)
	}
}
