package clikv

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
)

const Prefix = "cli."

var workspaceScopedKeys = map[string]bool{
	"default-chain":    true,
	"hitl-policy-name": true,
}

func Read(ctx context.Context, store runtimetypes.Store, key string) string {
	var val string
	if err := store.GetKV(ctx, Prefix+key, &val); err != nil {
		return ""
	}
	return strings.TrimSpace(val)
}

func ReadConfig(ctx context.Context, store runtimetypes.Store, workspaceID, key string) (string, string) {
	if workspaceScopedKeys[key] && workspaceID != "" {
		var val string
		if err := store.GetWorkspaceKV(ctx, workspaceID, Prefix+key, &val); err == nil {
			if v := strings.TrimSpace(val); v != "" {
				return v, "workspace"
			}
		}
	}
	return Read(ctx, store, key), "global"
}

func ReadHITLPolicy(ctx context.Context, store runtimetypes.Store) string {
	return Read(ctx, store, "hitl-policy-name")
}

func SetHITLPolicy(ctx context.Context, store runtimetypes.Store, name string) error {
	return SetString(ctx, store, "hitl-policy-name", name)
}

func SetString(ctx context.Context, store runtimetypes.Store, key, value string) error {
	v := strings.TrimSpace(value)
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return store.SetKV(ctx, Prefix+key, json.RawMessage(data))
}

func WriteConfig(ctx context.Context, store runtimetypes.Store, workspaceID, key, value string) error {
	v := strings.TrimSpace(value)
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if workspaceScopedKeys[key] && workspaceID != "" {
		return store.SetWorkspaceKV(ctx, workspaceID, Prefix+key, json.RawMessage(data))
	}
	return store.SetKV(ctx, Prefix+key, json.RawMessage(data))
}

const UnofficialSignInsKey = "unofficial-sign-ins"

func UnofficialSignIns(ctx context.Context, store runtimetypes.Store) bool {
	return strings.EqualFold(Read(ctx, store, UnofficialSignInsKey), "true")
}

const ToolImagesKeptKey = "tool-images-kept"

func ParseToolImagesKept(value string) (int, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a whole number of images, 0 or more (0 or empty sends every tool image again): got %q", ToolImagesKeptKey, value)
	}
	return n, nil
}

func ToolImagesKept(ctx context.Context, store runtimetypes.Store) int {
	n, err := ParseToolImagesKept(Read(ctx, store, ToolImagesKeptKey))
	if err != nil {
		return 0
	}
	return n
}

const EventRetentionDaysKey = "event-retention-days"

const ShippedEventRetentionDays = "30"

func ParseEventRetention(value string) (time.Duration, bool, error) {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" || v == "off" || v == "0" {
		return 0, false, nil
	}
	days, err := strconv.Atoi(v)
	if err != nil || days < 0 {
		return 0, false, fmt.Errorf("%s must be a whole number of days, or off: got %q", EventRetentionDaysKey, value)
	}
	return time.Duration(days) * 24 * time.Hour, true, nil
}

func EventRetention(ctx context.Context, store runtimetypes.Store) (time.Duration, bool, error) {
	return ParseEventRetention(Read(ctx, store, EventRetentionDaysKey))
}
