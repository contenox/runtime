package vfs

import (
	"crypto/sha256"
	"encoding/hex"
)

func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
