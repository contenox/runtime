package libauth_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/contenox/contenox/libs/libauth"
	// "github.com/contenox/contenox/libs/libcipher" //TODO remove me!!!
)

func TestAuthClaims_Valid(t *testing.T) {
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
			err := tt.claims.Valid()
			if (err != nil) != tt.wantErr {
				t.Errorf("AuthClaims.Valid() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateToken_Success(t *testing.T) {
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

func TestValidateToken_Valid(t *testing.T) {
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

func TestValidateToken_InvalidSignature(t *testing.T) {
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
func TestRefreshToken_Success(t *testing.T) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: "refresh_secret",
		JWTExpiry: time.Hour,
	}
	oldToken, _, _ := libauth.CreateToken(cfg, "user1", TestPermissions{})

	newToken, _, err := libauth.RefreshToken[TestPermissions](cfg, oldToken)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	// Parse new token to verify claims
	claims, err := libauth.ValidateToken[TestPermissions](context.Background(), newToken, cfg.JWTSecret)
	if err != nil {
		t.Fatalf("Validating refreshed token failed: %v", err)
	}
	if claims.Identity != "user1" {
		t.Errorf("Expected identity 'user1', got %q", claims.Identity)
	}
}

// func TestCheckPasswordHash_Correct(t *testing.T) {
// 	hash, _ := libcipher.NewHash(libcipher.GenerateHashArgs{
// 		Payload:    []byte("password"),
// 		SigningKey: []byte("key"),
// 		Salt:       []byte("salt"),
// 	}, sha256.New)

// 	ok, err := libcipher.CheckHash("key", "salt", "password", hash)
// 	if err != nil {
// 		t.Fatalf("CheckPasswordHash failed: %v", err)
// 	}
// 	if !ok {
// 		t.Error("Expected password to match hash")
// 	}
// }

type TestPermissions struct{}

func (t TestPermissions) RequireAuthorisation(forResource string, permission int) (bool, error) {
	return true, nil
}
