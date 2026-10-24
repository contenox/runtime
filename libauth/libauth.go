package libauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrNotAuthorized           = errors.New("libauth: not authorized")
	ErrTokenExpired            = errors.New("libauth: token has expired")
	ErrIssuedAtMissing         = errors.New("libauth: issued at claim is missing")
	ErrIssuedAtInFuture        = errors.New("libauth: token is malformed: issued-at time is in the future")
	ErrIdentityMissing         = errors.New("libauth: identity claim is missing")
	ErrInvalidTokenClaims      = errors.New("libauth: invalid token claims")
	ErrTokenMissing            = errors.New("libauth: token is missing")
	ErrUnexpectedSigningMethod = errors.New("libauth: unexpected signing method")
	ErrTokenParsingFailed      = errors.New("libauth: token parsing failed")
	ErrTokenSigningFailed      = errors.New("libauth: token signing failed")
	ErrEmptyJWTSecret          = errors.New("libauth: JWT secret cannot be empty")
	ErrEmptyIdentity           = errors.New("libauth: identity cannot be empty")
)

type CreateTokenArgs struct {
	JWTSecret string
	JWTExpiry time.Duration
}

type AuthClaims[T Authz] struct {
	jwt.RegisteredClaims
	Identity    string `json:"identity"`
	Permissions T      `json:"permissions"`
}

type Authz interface {
	RequireAuthorisation(forResource string, permission int) (bool, error)
}

const clockSkewLeeway = 60 * time.Second

func (c AuthClaims[T]) Validate() error { return c.Valid() }

func (c AuthClaims[T]) Valid() error {
	exp, err := c.GetExpirationTime()
	if err != nil {
		return fmt.Errorf("failed to parse expiration time: %w: %w", ErrTokenParsingFailed, err)
	}
	if exp == nil || time.Now().UTC().After(exp.Time) {
		return ErrTokenExpired
	}

	iat, err := c.GetIssuedAt()
	if err != nil {
		return fmt.Errorf("failed to parse issued at: %w: %w", ErrTokenParsingFailed, err)
	}
	if iat == nil {
		return ErrIssuedAtMissing
	}
	if time.Now().UTC().Add(clockSkewLeeway).Before(iat.Time) {
		return ErrIssuedAtInFuture
	}

	if c.Identity == "" {
		return ErrIdentityMissing
	}

	return nil
}

type contextKey string

const ContextTokenKey = contextKey("jwtToken")

func ValidateToken[T Authz](ctx context.Context, tokenStr string, jwtSecret string) (*AuthClaims[T], error) {
	token, err := jwt.ParseWithClaims(tokenStr, &AuthClaims[T]{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrUnexpectedSigningMethod
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("%w: %w", ErrTokenExpired, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrTokenParsingFailed, err)
	}
	claims, ok := token.Claims.(*AuthClaims[T])
	if !ok || !token.Valid {
		return nil, ErrInvalidTokenClaims
	}

	return claims, nil
}

func RefreshTokenWithGracePeriod[T Authz](cfg CreateTokenArgs, oldToken string, gracePeriod time.Duration) (string, bool, time.Time, error) {
	token, err := jwt.ParseWithClaims(oldToken, &AuthClaims[T]{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrUnexpectedSigningMethod
		}
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		return "", false, time.Time{}, fmt.Errorf("%w: %w", ErrTokenParsingFailed, err)
	}

	claims, ok := token.Claims.(*AuthClaims[T])
	if !ok || !token.Valid {
		return "", false, time.Time{}, ErrInvalidTokenClaims
	}

	expiryTime, err := claims.GetExpirationTime()
	if err != nil || expiryTime == nil {
		return "", false, time.Time{}, ErrTokenParsingFailed
	}
	if time.Until(expiryTime.Time) > gracePeriod {
		return oldToken, false, time.Time{}, nil
	}
	newToken, expiresAt, err := RefreshToken[T](cfg, oldToken)
	return newToken, true, expiresAt, err
}

func RefreshToken[T Authz](cfg CreateTokenArgs, oldToken string) (string, time.Time, error) {
	token, err := jwt.ParseWithClaims(oldToken, &AuthClaims[T]{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrUnexpectedSigningMethod
		}
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %w", ErrTokenParsingFailed, err)
	}

	claims, ok := token.Claims.(*AuthClaims[T])
	if !ok || !token.Valid {
		return "", time.Time{}, ErrInvalidTokenClaims
	}

	expiresAt := time.Now().Add(cfg.JWTExpiry).UTC()

	newClaims := AuthClaims[T]{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   claims.Subject,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Identity:    claims.Identity,
		Permissions: claims.Permissions,
	}

	newToken := jwt.NewWithClaims(jwt.SigningMethodHS256, newClaims)
	t, err := newToken.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		return "", expiresAt, fmt.Errorf("%w: %w", ErrTokenSigningFailed, err)
	}
	return t, expiresAt, nil
}

func CreateToken[T Authz](cfg CreateTokenArgs, identity string, payload T) (string, time.Time, error) {
	if cfg.JWTSecret == "" {
		return "", time.Time{}, ErrEmptyJWTSecret
	}
	if identity == "" {
		return "", time.Time{}, ErrEmptyIdentity
	}

	expiresAt := time.Now().UTC().Add(cfg.JWTExpiry)
	claims := AuthClaims[T]{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   identity,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Identity:    identity,
		Permissions: payload,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	final, err := token.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		return "", time.Time{}, err
	}
	return final, expiresAt, nil
}

func GetClaims[T Authz](ctx context.Context, jwtSecret string) (T, error) {
	var permClaims T
	token, ok := ctx.Value(ContextTokenKey).(string)
	if !ok {
		return permClaims, ErrTokenMissing
	}

	permList, err := ValidateToken[T](ctx, token, jwtSecret)
	if err != nil {
		return permClaims, fmt.Errorf("%w: %w", ErrInvalidTokenClaims, err)
	}

	return permList.Permissions, nil
}

func CheckAuthorisation[T Authz](ctx context.Context, jwtSecret string, forResource string, permission int) (bool, error) {
	permList, err := GetClaims[T](ctx, jwtSecret)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrNotAuthorized, err)
	}
	ok, err := permList.RequireAuthorisation(forResource, permission)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrNotAuthorized, err)
	}
	if !ok {
		return false, nil
	}

	return true, nil
}

func GetIdentity[T Authz](ctx context.Context, jwtSecret string) (string, error) {
	token, ok := ctx.Value(ContextTokenKey).(string)
	if !ok {
		return "", ErrInvalidTokenClaims
	}

	claims, err := ValidateToken[T](ctx, token, jwtSecret)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidTokenClaims, err)
	}

	return claims.Identity, nil
}
