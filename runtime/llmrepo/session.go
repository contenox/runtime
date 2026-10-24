package llmrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
)

type sessionKeyContextKeyType struct{}

var sessionKeyContextKey sessionKeyContextKeyType

func WithSessionKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyContextKey, key)
}

func SessionKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(sessionKeyContextKey).(string)
	return key
}

func DeriveSessionKey(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("contenox-session-cache-key:" + sessionID))
	return hex.EncodeToString(sum[:])
}

func effectiveSessionKey(ctx context.Context, req Request) string {
	if req.SessionKey != "" {
		return req.SessionKey
	}
	return SessionKeyFromContext(ctx)
}

type CacheHints struct {
	StableSystem     bool
	StableTools      bool
	StableHistoryLen int
	TTL              string
}

func providerCacheHints(hints *CacheHints, sessionKey string) libmodelprovider.CacheHints {
	out := libmodelprovider.CacheHints{
		SessionKey:   sessionKey,
		StableSystem: true,
		StableTools:  true,
	}
	if hints == nil {
		return out
	}
	out.StableSystem = hints.StableSystem
	out.StableTools = hints.StableTools
	out.StableHistoryLen = hints.StableHistoryLen
	if hints.TTL != "" {
		if d, err := time.ParseDuration(hints.TTL); err == nil && d > 0 {
			out.TTL = d
		}
	}
	return out
}

type canonicalToolOrder struct{}

func (canonicalToolOrder) Apply(cfg *libmodelprovider.ChatConfig) {
	sortToolsCanonically(cfg.Tools)
}

func sortToolsCanonically(tools []libmodelprovider.Tool) {
	sort.SliceStable(tools, func(i, j int) bool {
		return toolSortKey(tools[i]) < toolSortKey(tools[j])
	})
}

func toolSortKey(t libmodelprovider.Tool) string {
	name := ""
	if t.Function != nil {
		name = t.Function.Name
	}
	return t.Type + "\x00" + name
}

func withCanonicalRequestShape(opts []libmodelprovider.ChatArgument, hints libmodelprovider.CacheHints) []libmodelprovider.ChatArgument {
	out := make([]libmodelprovider.ChatArgument, 0, len(opts)+2)
	out = append(out, opts...)
	out = append(out, libmodelprovider.WithCacheHints(hints))
	out = append(out, canonicalToolOrder{})
	return out
}

type usageSessionKey struct{}

func WithUsageSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, usageSessionKey{}, sessionID)
}

func UsageSessionFromContext(ctx context.Context) string {
	sessionID, _ := ctx.Value(usageSessionKey{}).(string)
	return sessionID
}
