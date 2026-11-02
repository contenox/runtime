package envelopeapi_test

import (
	"encoding/json"
	"github.com/contenox/runtime/runtime/project"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/envelopeapi"
	"github.com/contenox/runtime/runtime/internal/hitlpolicyapi"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/stretchr/testify/require"
)

func TestUnit_EnvelopesList_ReportsTheShippedCatalog(t *testing.T) {
	mux := http.NewServeMux()
	envelopeapi.AddRoutes(mux, "", nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/envelopes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []envelopeapi.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	names := make([]string, 0, len(got))
	for _, envelope := range got {
		require.NotEmpty(t, envelope.Description, "%s carries its description", envelope.Name)
		names = append(names, envelope.Name)
	}
	require.ElementsMatch(t, []string{"read_only", "ask_always", "auto_edit", "default", "strict", "acp", "dev", "acpx", "oracle"}, names)
}

func TestUnit_EnvelopesList_AndPolicyListShowTheSameChoices(t *testing.T) {
	data := t.TempDir()
	generated := filepath.Join(data, hitlservice.GeneratedPolicyDir)
	require.NoError(t, os.MkdirAll(generated, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(generated, "hitl-policy-strict.json"), []byte(`{"//": "Refuse what is not named. Rendered from [envelopes.strict] in agents.toml.", "version": 1, "rules": []}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(generated, "hitl-policy-chat.json"), []byte(`{"version": 1, "rules": []}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(data, "hitl-policy-mine.json"), []byte(`{"version": 1, "default_action": "approve", "rules": []}`), 0o600))
	policies := hitlservice.NewFSPolicySource(generated, data)
	files, err := localfileservice.NewPrivileged(data)
	require.NoError(t, err)

	mux := http.NewServeMux()
	envelopeapi.AddRoutes(mux, data, policies)
	hitlpolicyapi.AddRoutes(mux, files, policies)
	server := httptest.NewServer(mux)
	defer server.Close()

	var envelopes []envelopeapi.Envelope
	resp, err := http.Get(server.URL + "/envelopes")
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelopes))
	resp.Body.Close()
	require.Equal(t, []envelopeapi.Envelope{
		{Name: "mine", Source: hitlservice.PolicyFromFile},
		{Name: "strict", Description: "Refuse what is not named.", Source: hitlservice.PolicyFromEnvelope},
	}, envelopes)

	var listed []string
	resp, err = http.Get(server.URL + "/hitl-policies/list")
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	resp.Body.Close()
	require.Equal(t, []string{"hitl-policy-mine.json", "hitl-policy-strict.json"}, listed, "Settings, Workspace lists exactly what Settings, Missions lists")

	resp, err = http.Get(server.URL + "/hitl-policies?name=hitl-policy-strict.json")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "a listed envelope can also be read, though it lives in .generated")
}

func TestUnit_EnvelopesList_AWorkspaceSeesItsOwnEnvelopes(t *testing.T) {
	data := t.TempDir()
	generated := filepath.Join(data, hitlservice.GeneratedPolicyDir)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".contenox"), 0o755))
	require.NoError(t, os.WriteFile(project.LayerConfigPath(root), []byte("[envelopes.local]\n"), 0o644))
	wsDir := filepath.Join(generated, "workspaces", project.LayerKey(root))
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(generated, "hitl-policy-strict.json"), []byte(`{"//": "Global. Rendered from [envelopes.strict] in agents.toml.", "version": 1, "rules": []}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "hitl-policy-local.json"), []byte(`{"//": "Only here. Rendered from [envelopes.local] in agents.toml.", "version": 1, "rules": []}`), 0o600))
	policies := hitlservice.NewFSPolicySource(generated, data)
	files, err := localfileservice.NewPrivileged(data)
	require.NoError(t, err)

	mux := http.NewServeMux()
	envelopeapi.AddRoutes(mux, data, policies)
	hitlpolicyapi.AddRoutes(mux, files, policies)
	server := httptest.NewServer(mux)
	defer server.Close()

	list := func(url string, out any) {
		resp, err := http.Get(url)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}

	var global, scoped []envelopeapi.Envelope
	list(server.URL+"/envelopes", &global)
	list(server.URL+"/envelopes?workspace="+url.QueryEscape(filepath.Join(root, "sub")), &scoped)
	require.Equal(t, []envelopeapi.Envelope{{Name: "strict", Description: "Global.", Source: hitlservice.PolicyFromEnvelope}}, global)
	require.Equal(t, []envelopeapi.Envelope{
		{Name: "local", Description: "Only here.", Source: hitlservice.PolicyFromEnvelope, Scope: hitlservice.ScopeWorkspace},
		{Name: "strict", Description: "Global.", Source: hitlservice.PolicyFromEnvelope},
	}, scoped)

	var files2 []string
	list(server.URL+"/hitl-policies/list?workspace="+url.QueryEscape(root), &files2)
	require.Equal(t, []string{"hitl-policy-local.json", "hitl-policy-strict.json"}, files2, "the policy list and the envelope list agree for a workspace too")

	resp, err := http.Get(server.URL + "/hitl-policies?name=hitl-policy-local.json&workspace=" + url.QueryEscape(root))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "a workspace's envelope can be read in that workspace")
	resp, err = http.Get(server.URL + "/hitl-policies?name=hitl-policy-local.json")
	require.NoError(t, err)
	resp.Body.Close()
	require.NotEqual(t, http.StatusOK, resp.StatusCode, "and is not readable without it")
}
