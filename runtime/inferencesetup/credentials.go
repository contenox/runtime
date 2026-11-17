package inferencesetup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo/vertex"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func prepareCredentials(request ProviderRequest) (ProviderRequest, error) {
	if request.CredentialSource == "preserve" {
		if request.ID == "" || request.APIKey != "" || request.APIKeyEnv != "" || request.CredentialFile != "" {
			return request, errors.New("preserve credentials requires an existing connection and no replacement values")
		}
		request.CredentialSource = ""
		return request, nil
	}
	if request.CredentialFile != "" {
		if request.CredentialSource != "import" || request.APIKey != "" || request.APIKeyEnv != "" || request.Provider != "bedrock" && request.Provider != "vertex-google" {
			return request, errors.New("credential import requires a selected Bedrock or Vertex JSON file")
		}
		file, err := os.Open(request.CredentialFile)
		if err != nil {
			return request, err
		}
		raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if err = errors.Join(err, file.Close()); err != nil {
			return request, err
		}
		if len(raw) > 1<<20 {
			return request, errors.New("credential document exceeds 1 MiB")
		}
		request.APIKey, request.CredentialSource = string(raw), "stored"
	}
	if request.CredentialSource == "import" {
		return request, errors.New("select a credential JSON file to import")
	}
	if request.APIKey != "" {
		switch request.Provider {
		case "bedrock":
			var credentials struct {
				AccessKey string `json:"access_key_id"`
				SecretKey string `json:"secret_access_key"`
			}
			if json.Unmarshal([]byte(request.APIKey), &credentials) != nil || credentials.AccessKey == "" || credentials.SecretKey == "" {
				return request, errors.New("Bedrock credential JSON requires access_key_id and secret_access_key; session_token is supported for temporary credentials")
			}
		case "vertex-google":
			if err := vertex.ValidateCredentialJSON(context.Background(), request.APIKey); err != nil {
				return request, errors.New("Vertex credential document is invalid; import a supported Google credential JSON file")
			}
		}
	}
	return request, nil
}

func (s *Service) connectionCredentials(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend) (string, string, error) {
	if backend.Type == modelauth.ProviderType {
		status, err := modelauth.New(s.db).Status(ctx, backend.ID)
		if err != nil {
			return "device", "needs-sign-in", err
		}
		return "device", status.State, nil
	}
	if backend.Type == "modeld" || backend.Type == "local" {
		return "none", "not-required", nil
	}
	var config runtimestate.ProviderConfig
	err := store.GetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), &config)
	if errors.Is(err, libdbexec.ErrNotFound) {
		err = store.GetKV(ctx, runtimestate.ProviderKeyPrefix+backend.Type, &config)
	}
	if err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
		return "", "unavailable", err
	}
	source := config.CredentialSource
	if source == "" {
		switch {
		case config.APIKey != "":
			source = "stored"
		case config.APIKeyEnv != "":
			source = "environment"
		case backend.Type == "bedrock" || backend.Type == "vertex-google":
			source = "ambient"
		default:
			source = "none"
		}
	}
	if source == "environment" && strings.TrimSpace(os.Getenv(config.APIKeyEnv)) == "" {
		return source, "missing", nil
	}
	if source == "none" && (backend.Type == "ollama" || backend.Type == "vllm") {
		return source, "not-required", nil
	}
	if source == "none" {
		return source, "unverified", nil
	}
	return source, "unverified", nil
}
