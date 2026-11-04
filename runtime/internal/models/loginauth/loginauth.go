package loginauth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/copilotauth"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type Authenticator interface {
	Status(ctx context.Context, id string) (modelauth.Status, error)
	Login(ctx context.Context, id string, show func(modelauth.DeviceCode) error) error
	Logout(ctx context.Context, id string) error
	Headers(ctx context.Context, id string) (http.Header, error)
}

func Supports(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case modelauth.ProviderType, copilotauth.ProviderType:
		return true
	}
	return false
}

func For(db libdb.DBManager, providerType string) (Authenticator, bool) {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case modelauth.ProviderType:
		return modelauth.New(db), true
	case copilotauth.ProviderType:
		return copilotauth.New(db), true
	}
	return nil, false
}

func IsLoginRequired(err error) bool {
	return errors.Is(err, modelauth.ErrLoginRequired) || errors.Is(err, copilotauth.ErrLoginRequired)
}

func IsNotAllowed(err error) bool {
	return errors.Is(err, copilotauth.ErrNotAllowed)
}

func DeleteCredentials(ctx context.Context, exec libdb.Exec, id string) error {
	if err := modelauth.DeleteCredentials(ctx, exec, id); err != nil {
		return err
	}
	return copilotauth.DeleteCredentials(ctx, exec, id)
}

func CredentialGeneration(ctx context.Context, store runtimetypes.Store, providerType, id string) (string, error) {
	if strings.EqualFold(providerType, copilotauth.ProviderType) {
		return copilotauth.CredentialGeneration(ctx, store, id)
	}
	return modelauth.CredentialGeneration(ctx, store, id)
}
