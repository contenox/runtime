package backendservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
	"github.com/google/uuid"
)

// ConnectionContract describes atomic connection and credential persistence.
var ConnectionContract = servicecontract.Contract{
	ID: "provider-connection", Summary: "Save a provider connection",
	Description: "Save a named endpoint and its credentials together. Model discovery is a separate operation whose failure leaves the saved connection available for repair.",
	Operations:  []servicecontract.Operation{{ID: "save", Summary: "Save connection and credentials"}},
}

// CredentialChange selects how saving a connection treats its existing credentials.
type CredentialChange string

const (
	// PreserveCredentials leaves the existing credential source unchanged.
	PreserveCredentials CredentialChange = "preserve"
	// ReplaceCredentials saves the explicitly supplied credential source.
	ReplaceCredentials CredentialChange = "replace"
)

// SaveConnectionRequest identifies a connection and its credential update.
// An empty ID creates a new connection with an independent credential record.
type SaveConnectionRequest struct {
	CredentialSource string
	ID               string
	Name             string
	Type             string
	Endpoint         string
	CredentialChange CredentialChange
	APIKey           string
	APIKeyEnv        string
}

// SaveConnection atomically persists a connection and its credential source.
func (s *service) SaveConnection(ctx context.Context, request SaveConnectionRequest) (*runtimetypes.Backend, error) {
	if request.CredentialChange != PreserveCredentials && request.CredentialChange != ReplaceCredentials {
		return nil, errors.New("credential change must be preserve or replace")
	}
	if request.CredentialChange == PreserveCredentials && (request.APIKey != "" || request.APIKeyEnv != "" || request.CredentialSource != "") {
		return nil, errors.New("preserving credentials cannot include a replacement")
	}
	if request.APIKey != "" && request.APIKeyEnv != "" {
		return nil, errors.New("provide an API key or an API-key environment variable, not both")
	}
	if request.CredentialSource != "" {
		if err := validateCredentialSource(request); err != nil {
			return nil, err
		}
	}
	backend := &runtimetypes.Backend{ID: strings.TrimSpace(request.ID), Name: strings.TrimSpace(request.Name), Type: strings.TrimSpace(request.Type), BaseURL: strings.TrimSpace(request.Endpoint)}
	if err := validate(backend); err != nil {
		return nil, err
	}
	tx, commit, release, err := s.dbInstance.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	store := runtimetypes.New(tx)
	creating := backend.ID == ""
	if creating {
		count, err := store.EstimateBackendCount(ctx)
		if err != nil {
			return nil, err
		}
		if err := store.EnforceMaxRowCount(ctx, count); err != nil {
			return nil, err
		}
		backend.ID = uuid.NewString()
		if err := store.CreateBackend(ctx, backend); err != nil {
			return nil, sanitizeBackendStoreError(backend, err)
		}
	} else {
		if err := store.LockBackend(ctx, backend.ID); err != nil {
			return nil, err
		}
		existing, err := store.GetBackend(ctx, backend.ID)
		if err != nil {
			return nil, err
		}
		if existing.Type != backend.Type {
			return nil, errors.New("provider type cannot be changed; connect another provider")
		}
		if err := store.UpdateBackend(ctx, backend); err != nil {
			return nil, sanitizeBackendStoreError(backend, err)
		}
	}
	if creating || request.CredentialChange == ReplaceCredentials {
		raw, err := json.Marshal(runtimestate.ProviderConfig{Type: backend.Type, APIKey: request.APIKey, APIKeyEnv: request.APIKeyEnv, CredentialSource: request.CredentialSource})
		if err != nil {
			return nil, err
		}
		if err := store.SetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), raw); err != nil {
			return nil, err
		}
	}
	saved, err := store.GetBackend(ctx, backend.ID)
	if err != nil {
		return nil, err
	}
	if err := commit(ctx); err != nil {
		return nil, err
	}
	return saved, nil
}

func validateCredentialSource(request SaveConnectionRequest) error {
	switch request.CredentialSource {
	case "stored":
		if strings.TrimSpace(request.APIKey) == "" || request.APIKeyEnv != "" {
			return errors.New("stored credentials require a key or imported credential document")
		}
	case "environment":
		if strings.TrimSpace(request.APIKeyEnv) == "" || request.APIKey != "" {
			return errors.New("environment credentials require an environment variable name")
		}
	case "ambient":
		if request.Type != "bedrock" && request.Type != "vertex-google" {
			return errors.New("this adapter does not support ambient credentials")
		}
		if request.APIKey != "" || request.APIKeyEnv != "" {
			return errors.New("ambient credentials cannot include stored or environment credentials")
		}
	case "none", "device":
		if request.APIKey != "" || request.APIKeyEnv != "" {
			return errors.New("selected credential source cannot include an API key")
		}
		if request.CredentialSource == "device" && request.Type != "openai-codex" {
			return errors.New("this adapter does not support subscription sign-in")
		}
		if request.CredentialSource == "none" && (request.Type == "bedrock" || request.Type == "vertex-google" || request.Type == "openai-codex") {
			return errors.New("select the required credential source for this adapter")
		}
	default:
		return errors.New("unknown credential source")
	}
	return nil
}
