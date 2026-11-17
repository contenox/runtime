package inferencesetup_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func mediaAnswer(message modelrepo.Message, fail string) (string, error) {
	if len(message.Images) > 0 {
		if fail == "images" {
			return "red", nil
		}
		img, err := png.Decode(bytes.NewReader(message.Images[0].Data))
		if err != nil {
			return "", err
		}
		red, _, blue, _ := img.At(0, 0).RGBA()
		if red > blue {
			return "red", nil
		}
		return "blue", nil
	}
	if fail == "audio" {
		return "2", nil
	}
	data := message.Audio[0].Data
	if message.Audio[0].MimeType != modelrepo.WAVMimeType || !modelrepo.IsWAV(data) || len(data) < 44 {
		return "", fmt.Errorf("invalid audio sample")
	}
	count, quiet := 0, 16000
	for i := 44; i+1 < len(data); i += 2 {
		value := int16(binary.LittleEndian.Uint16(data[i:]))
		if value > 1000 || value < -1000 {
			if quiet > 1000 {
				count++
			}
			quiet = 0
		} else {
			quiet++
		}
	}
	return fmt.Sprint(count), nil
}

func TestIntegration_Roles_MediaRequirementsVerifyAndPersistWithoutReplacingOnFailure(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "media.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body any = map[string]any{"name": "models/gemini-2.5-flash", "supportedGenerationMethods": []string{"generateContent"}, "inputTokenLimit": 32768}
		if r.URL.Path == "/v1beta/models" {
			body = map[string]any{"models": []map[string]string{{"name": "models/gemini-2.5-flash"}}}
		}
		require.NoError(t, json.NewEncoder(w).Encode(body))
	}))
	t.Cleanup(server.Close)
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	models := &verifyingModels{}
	setup, err := inferencesetup.New(db, state, bus, modelFactory{repo: models})
	require.NoError(t, err)
	connection, err := setup.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "media-account", Provider: "gemini", Endpoint: server.URL, APIKey: "fixture-key"})
	require.NoError(t, err)
	require.Empty(t, connection.DiscoveryError)
	configuration, err := setup.Configuration(ctx)
	require.NoError(t, err)
	require.Len(t, configuration.Models, 1)
	require.True(t, configuration.Models[0].Vision)
	require.True(t, configuration.Models[0].Audio)
	for _, role := range []string{inferencesetup.RoleChat, inferencesetup.RoleRecovery} {
		assignment := inferencesetup.Assignment{Role: role, Provider: runtimestate.ConnectionSelector(connection.ID), Model: configuration.Models[0].Name}
		for _, inputs := range []inferencesetup.InputRequirement{inferencesetup.InputText, inferencesetup.InputImages, inferencesetup.InputAudio, inferencesetup.InputImagesAndAudio} {
			assignment.Inputs = inputs
			verified, err := setup.Assign(ctx, assignment, inferencesetup.SourceUser)
			require.NoError(t, err)
			require.Equal(t, inputs, verified.Inputs)
		}
		restarted, err := inferencesetup.New(db, state, bus, modelFactory{repo: models})
		require.NoError(t, err)
		original, err := restarted.VerifiedRole(ctx, role)
		require.NoError(t, err)
		require.Equal(t, inferencesetup.InputImagesAndAudio, original.Inputs)
		for _, failure := range []string{"images", "audio"} {
			models.failInput = failure
			_, err = restarted.Verify(ctx, role)
			require.ErrorContains(t, err, "response did not identify")
			preserved, err := restarted.VerifiedRole(ctx, role)
			require.NoError(t, err)
			require.Equal(t, original, preserved)
			models.failInput = ""
		}
	}
	require.NoError(t, setup.InheritRecovery(ctx))
	recovery, err := setup.VerifiedRole(ctx, inferencesetup.RoleRecovery)
	require.NoError(t, err)
	require.True(t, recovery.Inherited)
	require.Equal(t, inferencesetup.InputImagesAndAudio, recovery.Inputs)
	require.Equal(t, inferencesetup.SourceUser, recovery.Source, "inheriting recovery is a user choice")
}
