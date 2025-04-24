package serverops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libauth"
)

const DefaultServerGroup = "server"
const DefaultDefaultServiceGroup = "admin_panel"
const DefaultAdminUser = "admin@admin.com"

func accessListFromStore(ctx context.Context, storeInstance store.Store, identity string) (store.AccessList, error) {
	var al store.AccessList
	al, err := storeInstance.GetAccessEntriesByIdentity(ctx, identity)
	if err != nil {
		return al, err
	}
	return al, nil
}

// CheckResourceAuthorization checks if the user has the required permission for a given resource.
func CheckResourceAuthorization(ctx context.Context, storeInstance store.Store, resource string, requiredPermission store.Permission) error {
	if instance := GetManagerInstance(); instance == nil {
		return fmt.Errorf("BUG: Service Manager was not initialized")
	}
	if instance := GetManagerInstance(); instance != nil && instance.IsSecurityEnabled(DefaultServerGroup) {
		identity, err := GetIdentity(ctx)
		if err != nil {
			return fmt.Errorf("unauthorized: failed to get user identity: %w", err)
		}
		authorized, err := checkAuth(ctx, identity, resource, requiredPermission, instance.GetSecret(), storeInstance)
		if err != nil {
			return err
		}
		if !authorized {
			return fmt.Errorf("unauthorized: user lacks permission %v for resource %s", requiredPermission, resource)
		}
	}
	return nil

}

func checkAuth(ctx context.Context, identity, resource string, requiredPermission store.Permission, secret string, storeInstance store.Store) (bool, error) {
	accessList, err := libauth.GetClaims[store.AccessList](ctx, secret)
	if err != nil {
		return false, fmt.Errorf("failed to get access list: %w", err)
	}
	authorized, err := accessList.RequireAuthorisation(resource, int(requiredPermission))
	if err != nil && !errors.Is(err, store.ErrAccessEntryNotFound) {
		return authorized, err
	}
	if errors.Is(err, store.ErrAccessEntryNotFound) {
		accessList, err = accessListFromStore(ctx, storeInstance, identity)
		if err != nil {
			return authorized, fmt.Errorf("failed to get access list: %w", err)
		}
		authorized, err = accessList.RequireAuthorisation(resource, int(requiredPermission))
		if errors.Is(err, store.ErrAccessEntryNotFound) {
			return authorized, fmt.Errorf("error during authorization check: %w", err)
		}
	}
	return authorized, nil
}

func CheckServiceAuthorization[T ServiceMeta](ctx context.Context, storeInstance store.Store, s T, permission store.Permission) error {
	instance := GetManagerInstance()
	if instance == nil {
		return fmt.Errorf("BUG: Service Manager was not initialized")
	}
	if !instance.IsSecurityEnabled(DefaultServerGroup) {
		return nil
	}
	identity, err := GetIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get user identity: %w", err)
	}
	tryAuth := []string{
		s.GetServiceName(),
		s.GetServiceGroup(),
		DefaultServerGroup,
	}
	var authorized bool
	for _, resource := range tryAuth {
		authorized, err = checkAuth(ctx, identity, resource, permission, instance.GetSecret(), storeInstance)
		if err != nil && !errors.Is(err, store.ErrAccessEntryNotFound) {
			return err
		}
		if authorized {
			break
		}
	}
	if authorized {
		return nil
	}
	return fmt.Errorf("service %s is not authorized: %w", s.GetServiceName(), libauth.ErrNotAuthorized)
}

func CreateAuthToken(subject string, permissions store.AccessList) (string, time.Time, error) {
	instance := GetManagerInstance()
	if instance == nil {
		return "", time.Time{}, fmt.Errorf("BUG: Service Manager was not initialized")
	}

	cfg := libauth.CreateTokenArgs{
		JWTSecret: instance.GetSecret(),
		JWTExpiry: instance.GetTokenExpiry(),
	}
	// Delegate token creation to libauth.
	token, expiresAt, err := libauth.CreateToken[store.AccessList](cfg, subject, permissions)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create token: %w", err)
	}
	return token, expiresAt, nil
}

func RefreshToken(ctx context.Context) (string, bool, time.Time, error) {
	tokenString, ok := ctx.Value(libauth.ContextTokenKey).(string)
	if !ok {
		return "", false, time.Time{}, fmt.Errorf("BUG: token not found in context")
	}
	gracePeriod := time.Minute * 20
	return RefreshPlainToken(ctx, tokenString, &gracePeriod)
}

func RefreshPlainToken(ctx context.Context, token string, withGracePeriod *time.Duration) (string, bool, time.Time, error) {
	instance := GetManagerInstance()
	if instance == nil {
		return "", false, time.Time{}, fmt.Errorf("BUG: Service Manager was not initialized")
	}
	if !instance.IsSecurityEnabled(DefaultServerGroup) {
		return "", false, time.Time{}, nil
	}
	cfg := libauth.CreateTokenArgs{
		JWTSecret: instance.GetSecret(),
		JWTExpiry: instance.GetTokenExpiry(),
	}
	if withGracePeriod == nil {
		tokenString, expiresAt, err := libauth.RefreshToken[store.AccessList](cfg, token)
		if err != nil {
			return "", false, time.Time{}, fmt.Errorf("failed to refresh token: %w", err)
		}
		return tokenString, true, expiresAt, nil
	}

	tokenString, wasReplaced, expiresAt, err := libauth.RefreshTokenWithGracePeriod[store.AccessList](cfg, token, *withGracePeriod)
	if err != nil {
		return "", false, time.Time{}, fmt.Errorf("failed to refresh token: %w", err)
	}

	return tokenString, wasReplaced, expiresAt, nil
}

// GetIdentity extracts the identity from the context using the JWT secret from the ServiceManager.
func GetIdentity(ctx context.Context) (string, error) {
	manager := GetManagerInstance()
	if manager == nil {
		return "", fmt.Errorf("service manager is not initialized")
	}
	if !manager.IsSecurityEnabled(DefaultServerGroup) {
		return DefaultAdminUser, nil
	}
	jwtSecret := manager.GetSecret()
	if jwtSecret == "" {
		return "", libauth.ErrEmptyJWTSecret
	}

	return libauth.GetIdentity[store.AccessList](ctx, jwtSecret)
}
