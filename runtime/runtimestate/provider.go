package runtimestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
)

const ProviderKeyPrefix = "cloud-provider:"

const (
	CredentialStored      = "stored"
	CredentialEnvironment = "environment"
	CredentialAmbient     = "ambient"
	CredentialNone        = "none"
	CredentialDevice      = "device"
)

type ProviderConfig struct {
	CredentialSource string
	APIKey           string
	APIKeyEnv        string
	Type             string
}

type EnvironmentUnsetError struct {
	Variable string
}

func (e *EnvironmentUnsetError) Error() string {
	return fmt.Sprintf("environment variable %s is unset or empty", e.Variable)
}

func (pc ProviderConfig) Source(backendType string) string {
	switch {
	case pc.CredentialSource != "":
		return pc.CredentialSource
	case strings.TrimSpace(pc.APIKeyEnv) != "":
		return CredentialEnvironment
	case pc.APIKey != "":
		return CredentialStored
	}
	switch modelrepo.CanonicalBackendType(backendType) {
	case "bedrock", "vertex-google":
		return CredentialAmbient
	}
	return CredentialNone
}

func (pc ProviderConfig) Credential(backendType string) (string, error) {
	switch source := pc.Source(backendType); source {
	case CredentialStored:
		if pc.APIKey == "" {
			return "", errors.New("the credential record says its key is stored but holds none")
		}
		return pc.APIKey, nil
	case CredentialEnvironment:
		name := strings.TrimSpace(pc.APIKeyEnv)
		if name == "" {
			return "", errors.New("the credential record names an environment source but no variable")
		}
		value := os.Getenv(name)
		if strings.TrimSpace(value) == "" {
			return "", &EnvironmentUnsetError{Variable: name}
		}
		return value, nil
	case CredentialAmbient, CredentialNone, CredentialDevice:
		return "", nil
	default:
		return "", fmt.Errorf("the credential record names an unknown credential source %q", source)
	}
}

func (pc ProviderConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		CredentialSource string `json:"CredentialSource,omitempty"`
		APIKey           string `json:"APIKey"`
		APIKeyEnv        string `json:"APIKeyEnv,omitempty"`
		Type             string `json:"Type"`
	}{
		CredentialSource: pc.CredentialSource,
		APIKey:           pc.APIKey,
		APIKeyEnv:        pc.APIKeyEnv,
		Type:             pc.Type,
	})
}
