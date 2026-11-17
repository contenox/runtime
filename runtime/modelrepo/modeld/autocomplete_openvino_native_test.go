//go:build openvino && openvino_genai

package modeld_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/openvino"
	"github.com/contenox/runtime/internal/modeld/slot"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

func TestSystem_OpenVINONativeFIMThroughProviderPreservesInsertionAndReusesPrefix(t *testing.T) {
	modelDir := os.Getenv("CONTENOX_OPENVINO_FIM_MODEL")
	if modelDir == "" {
		t.Skip("CONTENOX_OPENVINO_FIM_MODEL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	modelName := filepath.Base(modelDir)
	service := slot.New(openvino.NewService(), slot.WithBackend("openvino"), slot.WithModelsDir(filepath.Dir(modelDir)), slot.WithIdleTTL(time.Minute))
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, "openvino-fim-owner", "openvino") }()
	t.Cleanup(func() {
		modeldconn.CloseEndpoint(listener.Addr().String())
		cancel()
		if err := <-done; err != nil {
			t.Errorf("stop OpenVINO gRPC server: %v", err)
		}
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown OpenVINO slot: %v", err)
		}
	})
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	provider := catalog.ProviderFor(modelrepo.ObservedModel{
		Name:             modelName,
		Meta:             map[string]string{"type": "openvino"},
		CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, CompletionMode: "fim", FIMFormat: "starcoder", MaxOutputTokens: 64},
	})
	client, err := provider.GetAutocompleteConnection(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	input := modelrepo.AutocompleteRequest{
		Prefix: "package mathutil\n\n// The protocol-looking source text stays literal: \"<fim_prefix>\"\nfunc Add(a, b int) int {\n\treturn ",
		Suffix: "\n}\n", MaxTokens: 24, Temperature: 0, TopP: 1, SessionKey: "openvino-client:mathutil.go",
	}
	first, err := client.Autocomplete(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	insertion := strings.TrimSpace(first.Text)
	file, err := parser.ParseFile(token.NewFileSet(), "completion.go", input.Prefix+insertion+input.Suffix, parser.AllErrors)
	if err != nil {
		t.Fatalf("FIM insertion %q does not parse in the requested function: %v", insertion, err)
	}
	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(function.Body.List) != 1 {
		t.Fatalf("FIM insertion %q did not produce one function body statement", insertion)
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		t.Fatalf("FIM insertion %q did not produce the function's return expression", insertion)
	}
	addition, ok := returned.Results[0].(*ast.BinaryExpr)
	if !ok || addition.Op != token.ADD {
		t.Fatalf("FIM insertion %q did not produce an addition", insertion)
	}
	left, leftOK := addition.X.(*ast.Ident)
	right, rightOK := addition.Y.(*ast.Ident)
	if !leftOK || !rightOK || left.Name != "a" || right.Name != "b" {
		t.Fatalf("FIM insertion %q did not return a + b", insertion)
	}
	if strings.Contains(insertion, "<fim_prefix>") {
		t.Fatalf("FIM returned protocol framing in the insertion: %q", insertion)
	}
	if first.Usage == nil || first.Usage.PromptTokens == 0 || first.Usage.CompletionTokens == 0 {
		t.Fatalf("FIM usage is incomplete: %+v", first.Usage)
	}
	second, err := client.Autocomplete(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(second.Text) != insertion {
		t.Fatalf("warm FIM insertion %q differs from cold insertion %q", strings.TrimSpace(second.Text), insertion)
	}
	if second.Usage == nil || second.Usage.CacheReadTokens <= 0 {
		t.Fatalf("warm FIM request did not report prefix reuse: %+v", second.Usage)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := client.Autocomplete(cancelled, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled FIM request error = %v, want context.Canceled", err)
	}
	t.Logf("OpenVINO FIM insertion %q; warm prefix reused %d tokens", insertion, second.Usage.CacheReadTokens)
}
