package libauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contenox/runtime/libauth"
	"github.com/golang-jwt/jwt/v5"
)

func TestUnit_AuthClaims_Valid(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(2 * time.Hour)
	past := now.Add(-2 * time.Hour)

	tests := []struct {
		name    string
		claims  libauth.AuthClaims[TestPermissions]
		wantErr bool
	}{
		{
			name: "valid claims",
			claims: libauth.AuthClaims[TestPermissions]{
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
					IssuedAt:  jwt.NewNumericDate(now),
				},
				Identity:    "user1",
				Permissions: TestPermissions{},
			},
			wantErr: false,
		},
		{
			name: "expired token",
			claims: libauth.AuthClaims[TestPermissions]{
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(past),
					IssuedAt:  jwt.NewNumericDate(past),
				},
				Identity:    "user1",
				Permissions: TestPermissions{},
			},
			wantErr: true,
		},
		{
			name: "missing expiration",
			claims: libauth.AuthClaims[TestPermissions]{
				RegisteredClaims: jwt.RegisteredClaims{
					IssuedAt: jwt.NewNumericDate(now),
				},
				Identity:    "user1",
				Permissions: TestPermissions{},
			},
			wantErr: true,
		},
		{
			name: "future issued at",
			claims: libauth.AuthClaims[TestPermissions]{
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(future),
					IssuedAt:  jwt.NewNumericDate(future),
				},
				Identity:    "user1",
				Permissions: TestPermissions{},
			},
			wantErr: true,
		},
		{
			name: "missing identity",
			claims: libauth.AuthClaims[TestPermissions]{
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
					IssuedAt:  jwt.NewNumericDate(now),
				},
				Identity:    "",
				Permissions: TestPermissions{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.claims.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("AuthClaims.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUnit_CreateToken_Success(t *testing.T) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: "testsecret",
		JWTExpiry: time.Hour,
	}
	identity := "testuser"
	perms := TestPermissions{}

	tokenStr, _, err := libauth.CreateToken(cfg, identity, perms)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	token, err := jwt.ParseWithClaims(tokenStr, &libauth.AuthClaims[TestPermissions]{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		t.Fatalf("Failed to parse token: %v", err)
	}

	if claims, ok := token.Claims.(*libauth.AuthClaims[TestPermissions]); ok && token.Valid {
		if claims.Identity != identity {
			t.Errorf("Expected identity %q, got %q", identity, claims.Identity)
		}
	} else {
		t.Error("Invalid token claims")
	}
}

func TestUnit_ValidateToken_Valid(t *testing.T) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: "valid_secret",
		JWTExpiry: time.Hour,
	}
	tokenStr, _, _ := libauth.CreateToken(cfg, "user1", TestPermissions{})

	claims, err := libauth.ValidateToken[TestPermissions](context.Background(), tokenStr, cfg.JWTSecret)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if claims.Identity != "user1" {
		t.Errorf("Expected identity 'user1', got %q", claims.Identity)
	}
}

func TestUnit_ValidateToken_InvalidSignature(t *testing.T) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: "valid_secret",
		JWTExpiry: time.Hour,
	}
	tokenStr, _, _ := libauth.CreateToken(cfg, "user1", TestPermissions{})

	_, err := libauth.ValidateToken[TestPermissions](context.Background(), tokenStr, "wrong_secret")
	if err == nil {
		t.Error("Expected error for invalid signature")
	}
}
func TestUnit_RefreshToken_Success(t *testing.T) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: "refresh_secret",
		JWTExpiry: time.Hour,
	}
	oldToken, _, _ := libauth.CreateToken(cfg, "user1", TestPermissions{})

	newToken, _, err := libauth.RefreshToken[TestPermissions](cfg, oldToken)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	claims, err := libauth.ValidateToken[TestPermissions](context.Background(), newToken, cfg.JWTSecret)
	if err != nil {
		t.Fatalf("Validating refreshed token failed: %v", err)
	}
	if claims.Identity != "user1" {
		t.Errorf("Expected identity 'user1', got %q", claims.Identity)
	}
}

type TestPermissions struct{}

func (t TestPermissions) RequireAuthorisation(forResource string, permission int) (bool, error) {
	return true, nil
}

var _ jwt.ClaimsValidator = libauth.AuthClaims[TestPermissions]{}

func TestUnit_ValidateToken_ExpiredSurfacesAsErrTokenExpired(t *testing.T) {
	cfg := libauth.CreateTokenArgs{JWTSecret: "test_secret_test_secret", JWTExpiry: -time.Hour}
	tokenStr, _, err := libauth.CreateToken(cfg, "user1", TestPermissions{})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	_, err = libauth.ValidateToken[TestPermissions](context.Background(), tokenStr, cfg.JWTSecret)
	if !errors.Is(err, libauth.ErrTokenExpired) {
		t.Fatalf("expired token should surface ErrTokenExpired (401), got %v", err)
	}
	if errors.Is(err, libauth.ErrTokenParsingFailed) {
		t.Fatalf("expired token must not read as a parse failure (400): %v", err)
	}
}

func TestUnit_ValidateToken_RejectsEmptyIdentity(t *testing.T) {
	const secret = "test_secret_test_secret"
	claims := libauth.AuthClaims[TestPermissions]{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
		Identity: "",
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := libauth.ValidateToken[TestPermissions](context.Background(), tokenStr, secret); !errors.Is(err, libauth.ErrIdentityMissing) {
		t.Fatalf("empty identity should be rejected, got %v", err)
	}
}

func TestUnit_ValidateToken_ToleratesModestClockSkew(t *testing.T) {
	const secret = "test_secret_test_secret"
	claims := libauth.AuthClaims[TestPermissions]{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC().Add(5 * time.Second)),
		},
		Identity: "user1",
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := libauth.ValidateToken[TestPermissions](context.Background(), tokenStr, secret); err != nil {
		t.Fatalf("a 5s clock skew must not reject a valid token: %v", err)
	}
}

func TestUnit_SigningMethodIsPinnedOnEveryParsePath(t *testing.T) {
	const secret = "test_secret_test_secret"

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, libauth.AuthClaims[TestPermissions]{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
		Identity: "attacker",
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}

	cfg := libauth.CreateTokenArgs{JWTSecret: secret, JWTExpiry: time.Hour}

	if _, err := libauth.ValidateToken[TestPermissions](context.Background(), unsigned, secret); err == nil {
		t.Fatal("ValidateToken accepted an alg:none token")
	}
	if _, _, err := libauth.RefreshToken[TestPermissions](cfg, unsigned); err == nil {
		t.Fatal("RefreshToken accepted an alg:none token")
	}
	if _, _, _, err := libauth.RefreshTokenWithGracePeriod[TestPermissions](cfg, unsigned, time.Minute); err == nil {
		t.Fatal("RefreshTokenWithGracePeriod accepted an alg:none token")
	}
}
