package serverapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"strings"
	"time"

	"github.com/contenox/runtime/libauth"
	"golang.org/x/crypto/hkdf"
)

const (
	localOperatorIdentity = "local-operator"

	sessionTokenTTL = 24 * time.Hour

	sessionSecretSalt = "contenox-serve-session-v1"
	sessionSecretInfo = "session-jwt-hs256"
)

type localOperatorAuthz struct{}

func (localOperatorAuthz) RequireAuthorisation(string, int) (bool, error) {
	return true, nil
}

func deriveSessionSecret(token string) string {
	r := hkdf.New(sha256.New, []byte(token), []byte(sessionSecretSalt), []byte(sessionSecretInfo))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(out)
}

func mintSessionToken(token string) (string, time.Time, error) {
	cfg := libauth.CreateTokenArgs{
		JWTSecret: deriveSessionSecret(token),
		JWTExpiry: sessionTokenTTL,
	}
	return libauth.CreateToken(cfg, localOperatorIdentity, localOperatorAuthz{})
}

func validateSessionToken(token, jwtStr string) bool {
	jwtStr = strings.TrimSpace(jwtStr)
	if token == "" || jwtStr == "" {
		return false
	}
	_, err := libauth.ValidateToken[localOperatorAuthz](context.Background(), jwtStr, deriveSessionSecret(token))
	return err == nil
}

func AuthenticateCredential(token, cred string) bool {
	token = strings.TrimSpace(token)
	cred = strings.TrimSpace(cred)
	if token == "" || cred == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(cred), []byte(token)) == 1 {
		return true
	}
	return validateSessionToken(token, cred)
}
