// Package snapshotauth identifies a persistent Runtime session to Modeld on
// the same machine. Anyone with access to the data root already has access to
// its private snapshot files. A transport client needs proof of that access.
package snapshotauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

type Key []byte

const keyFile = "modeld-snapshot.key"

func read(root string) Key {
	if root == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, keyFile))
	if err != nil || len(b) != sha256.Size {
		return nil
	}
	return b
}

// Open lets concurrent local workers share a complete, private key. Failure
// disables reconnection of durable snapshots; live sessions can still run.
func Open(root string) Key {
	if key := read(root); key != nil {
		return key
	}
	if root == "" {
		return nil
	}
	f, err := os.CreateTemp(root, ".modeld-snapshot-key-*")
	if err != nil {
		return nil
	}
	defer os.Remove(f.Name())
	var key [sha256.Size]byte
	rand.Read(key[:])
	_, err = f.Write(key[:])
	if closeErr := f.Close(); err != nil || closeErr != nil {
		return nil
	}
	// Link publishes without replacing a key another worker just installed.
	// A reader can never observe a partially written key.
	if err := os.Link(f.Name(), filepath.Join(root, keyFile)); err != nil && !os.IsExist(err) {
		return nil
	}
	return read(root)
}

func (k Key) sum(label, namespace string) []byte {
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write([]byte(namespace))
	return mac.Sum(nil)
}

// Proof authorizes this session for the current local owner and its children.
// The caller selects local routes using the lease before reading the key.
func Proof(root, namespace, instance string) string {
	epoch, _, _ := strings.Cut(instance, "/")
	key := read(root)
	if len(key) == 0 || namespace == "" || epoch == "" {
		return ""
	}
	return hex.EncodeToString(key.sum("owner:"+epoch, namespace))
}

type scopeKey struct{}

// Authenticate verifies a proof before placing the stable snapshot identity
// into the server's context. Forged proofs leave ordinary isolation intact.
func (k Key) Authenticate(ctx context.Context, proof, namespace, instance string) (context.Context, bool) {
	epoch, _, child := strings.Cut(instance, "/")
	if child && strings.HasPrefix(namespace, epoch+"/") {
		// The shared owner fenced the live namespace with its epoch/connection
		// before forwarding the request to this managed child.
		_, inner, ok := strings.Cut(strings.TrimPrefix(namespace, epoch+"/"), "/")
		if ok {
			namespace = inner
		}
	}
	raw, err := hex.DecodeString(proof)
	if err != nil || len(k) != sha256.Size || epoch == "" || namespace == "" || !hmac.Equal(raw, k.sum("owner:"+epoch, namespace)) {
		return ctx, false
	}
	scope := hex.EncodeToString(k.sum("snapshot", namespace))
	return context.WithValue(ctx, scopeKey{}, scope), true
}

func Scope(ctx context.Context) string {
	scope, _ := ctx.Value(scopeKey{}).(string)
	return scope
}
