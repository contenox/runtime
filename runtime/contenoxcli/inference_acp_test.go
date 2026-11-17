package contenoxcli

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestIntegration_InferenceACPSetupOnly(t *testing.T) {
	t.Setenv("CONTENOX_DATA_ROOT", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	db, err := OpenDBAt(ctx, filepath.Join(t.TempDir(), "local.db"))
	require.NoError(t, err)
	defer db.Close()
	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer bus.Close()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	agent := libacp.NewAgentSideConnection(server, acpsvc.New(acpsvc.Deps{
		DB: db, OpenInference: inferenceSetupFactory(db, bus, nil, nil, libtracker.NoopTracker{}, ""),
	}))
	peer := libacp.NewClientSideConnection(client, func(*libacp.ClientSideConnection) libacp.Client {
		return &libacp.UnimplementedClient{}
	})
	updates := make(chan json.RawMessage, 4)
	peer.SetExtNotificationHandler(func(_ context.Context, method string, raw json.RawMessage) {
		if method == "_contenox/operation" {
			updates <- raw
		}
	})
	go func() { _ = agent.Run(ctx) }()
	go func() { _ = peer.Run(ctx) }()
	call := func(params string) (json.RawMessage, error) {
		return peer.CallExtMethod(ctx, "_contenox/inference", json.RawMessage(params))
	}
	status, err := call(`{"action":"status"}`)
	require.NoError(t, err)
	var snapshot protocolv1.InferenceResult
	require.NoError(t, protojson.Unmarshal(status, &snapshot))
	require.Len(t, snapshot.Roles, 5)
	require.NotEmpty(t, snapshot.ConfigurationContract.Operations)
	var local *protocolv1.InferenceProviderCapability
	for _, capability := range snapshot.ProviderTypes {
		if capability.Provider == "modeld" {
			local = capability
		}
	}
	require.Equal(t, "local", local.GetDefaultBaseUrl())
	require.Equal(t, "none", local.GetDefaultCredentialSource())
	var catalog []struct {
		Name string      `json:"name"`
		Size json.Number `json:"sizeBytes"`
	}
	raw, err := call(`{"action":"catalog"}`)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &catalog))
	require.NotEmpty(t, catalog)
	require.NotEmpty(t, catalog[0].Name)
	require.NotEmpty(t, catalog[0].Size)
	_, err = call(`{"action":"set-role-enabled","role":"embedding","enabled":false}`)
	require.NoError(t, err)
	_, err = call(`{"action":"set-role-enabled","role":"embedding","enabled":true}`)
	require.ErrorContains(t, err, "choose and verify a model")
	_, err = call(`{"action":"set-role-enabled","role":"primary","enabled":false}`)
	require.ErrorContains(t, err, "optional roles")
	_, err = call(`{"action":"apply-roles"}`)
	require.ErrorContains(t, err, "proposals")
	_, err = call(`{"action":"status","undeclared":true}`)
	require.ErrorContains(t, err, "undeclared")
	raw, err = call(`{"action":"auto-recommend","recommendation":{"mode":"local"}}`)
	require.NoError(t, err)
	require.NoError(t, protojson.Unmarshal(raw, &snapshot))
	plan := snapshot.GetAutomaticSetup().GetPlan()
	require.NotEmpty(t, plan.GetFingerprint())
	require.NotEmpty(t, plan.GetRoles())
	request, err := protojson.Marshal(&protocolv1.InferenceRequest{
		Action: "auto-apply", Plan: plan, SelectedRoles: []string{"primary"}, OperationId: "apply-test",
	})
	require.NoError(t, err)
	_, err = call(string(request))
	require.ErrorContains(t, err, "needs a resolved proposal")
	for _, want := range []string{"running", "failed"} {
		select {
		case update := <-updates:
			var progress struct{ State, OperationID string }
			require.NoError(t, json.Unmarshal(update, &progress))
			require.Equal(t, "apply-test", progress.OperationID)
			require.Equal(t, want, progress.State)
		case <-ctx.Done():
			t.Fatal("missing operation update", want)
		}
	}
	script := filepath.Join(t.TempDir(), "dialog.json")
	require.NoError(t, os.WriteFile(script, []byte(`{"model":"scripted-test","embed_dimensions":8,"turns":[{"text":"answer","finish_reason":"stop"}]}`), 0o600))
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{
		ID: "embedding-fixture", Name: "fixture", Type: modelrepo.ScriptedTestBackendType, BaseURL: script,
	}))
	_, err = call(`{"action":"refresh","provider":"scripted-test"}`)
	require.NoError(t, err)
	_, err = call(`{"action":"assign","role":"embedding","provider":"scripted-test","model":"scripted-test"}`)
	require.NoError(t, err)
	status, err = call(`{"action":"status"}`)
	require.NoError(t, err)
	require.NoError(t, protojson.Unmarshal(status, &snapshot))
	for _, role := range snapshot.Roles {
		if role.Id == "embedding" {
			require.Equal(t, "scripted-test", role.Model)
			require.Equal(t, int32(8), role.GetVerification().GetDimension())
			return
		}
	}
	t.Fatal("missing verified embedding role")
}
