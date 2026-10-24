package libcipher

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	SigningSeedSize       = ed25519.SeedSize
	SigningPublicKeySize  = ed25519.PublicKeySize
	SigningPrivateKeySize = ed25519.PrivateKeySize
	SignatureSize         = ed25519.SignatureSize
)

type (
	SigningPublicKey  = ed25519.PublicKey
	SigningPrivateKey = ed25519.PrivateKey
)

type SigningKeyError string

func (e SigningKeyError) Error() string {
	return "libcipher: " + (string)(e)
}

const (
	ErrBadPublicKey  = SigningKeyError("not an ed25519 public key")
	ErrBadPrivateKey = SigningKeyError("not an ed25519 private key")
	ErrBadSeed       = SigningKeyError("not an ed25519 seed")
	ErrKeyGeneration = SigningKeyError("error generating signing key")
)

var SigningKeyEncoding = base64.StdEncoding

func GenerateSigningKey() (SigningPublicKey, SigningPrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w:%w", ErrKeyGeneration, err)
	}
	return pub, priv, nil
}

func FormatPublicKey(pub SigningPublicKey) string {
	if len(pub) != SigningPublicKeySize {
		return ""
	}
	return SigningKeyEncoding.EncodeToString(pub)
}

func ParsePublicKey(s string) (SigningPublicKey, error) {
	b, err := decodeKeyText(s, SigningPublicKeySize, ErrBadPublicKey)
	if err != nil {
		return nil, err
	}
	return SigningPublicKey(b), nil
}

func FormatSigningSeed(priv SigningPrivateKey) (string, error) {
	if len(priv) != SigningPrivateKeySize {
		return "", fmt.Errorf("%w: %d bytes, want %d", ErrBadPrivateKey, len(priv), SigningPrivateKeySize)
	}
	return SigningKeyEncoding.EncodeToString(priv.Seed()), nil
}

func ParseSigningSeed(s string) (SigningPublicKey, SigningPrivateKey, error) {
	seed, err := decodeKeyText(s, SigningSeedSize, ErrBadSeed)
	if err != nil {
		return nil, nil, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(SigningPublicKey), priv, nil
}

func Sign(priv SigningPrivateKey, message []byte) ([]byte, error) {
	if len(priv) != SigningPrivateKeySize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrBadPrivateKey, len(priv), SigningPrivateKeySize)
	}
	return ed25519.Sign(priv, message), nil
}

func Verify(pub SigningPublicKey, message, sig []byte) bool {
	if len(pub) != SigningPublicKeySize || len(sig) != SignatureSize {
		return false
	}
	return ed25519.Verify(pub, message, sig)
}

func EqualSigningKey(a, b SigningPrivateKey) bool {
	return hmac.Equal(a, b)
}

func decodeKeyText(s string, want int, badKey SigningKeyError) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty", badKey)
	}
	if len(s) == want*2 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	gotLen := -1
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		b, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		if len(b) != want {
			gotLen = len(b)
			continue
		}
		return b, nil
	}
	if gotLen >= 0 {
		return nil, fmt.Errorf("%w: %d bytes, want %d", badKey, gotLen, want)
	}
	return nil, fmt.Errorf("%w: not base64 or hex", badKey)
}
