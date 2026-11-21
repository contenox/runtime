package clikv

import (
	"context"
	"encoding/json"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"strings"
)

const Prefix = "cli."

// EnabledRoleKey stores an optional inference role's on/off selection.
func EnabledRoleKey(role string) string { return "inference.enabled." + role }

func SetString(ctx context.Context, store runtimetypes.Store, key, value string) error {
	v := strings.TrimSpace(value)
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return store.SetKV(ctx, Prefix+key, json.RawMessage(data))
}
