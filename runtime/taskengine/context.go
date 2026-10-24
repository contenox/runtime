package taskengine

import (
	"context"
	"fmt"
	"maps"

	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
)

type templateVarsKey struct{}

const TemplateVarModelPinned = "model_pinned"

func WithTemplateVars(ctx context.Context, vars map[string]string) context.Context {
	if vars == nil {
		return ctx
	}
	return context.WithValue(ctx, templateVarsKey{}, vars)
}

func TemplateVarsFromContext(ctx context.Context) (map[string]string, error) {
	v, ok := ctx.Value(templateVarsKey{}).(map[string]string)
	if !ok {
		return nil, fmt.Errorf("template vars not set in context")
	}
	return v, nil
}

func MergeTemplateVars(ctx context.Context, overlay map[string]string) context.Context {
	base := make(map[string]string)
	if existing, err := TemplateVarsFromContext(ctx); err == nil && existing != nil {
		maps.Copy(base, existing)
	}
	maps.Copy(base, overlay)
	return WithTemplateVars(ctx, base)
}

func WithRequestedContextLength(ctx context.Context, contextLength int) context.Context {
	return libmodelprovider.WithRequestedContextLength(ctx, contextLength)
}

func RequestedContextLengthFromContext(ctx context.Context) int {
	return libmodelprovider.RequestedContextLengthFromContext(ctx)
}

type runtimeToolsAllowlistKey struct{}

type runtimeToolsAllowlist struct {
	list []string
}

func WithRuntimeToolsAllowlist(ctx context.Context, allowlist []string) context.Context {
	return context.WithValue(ctx, runtimeToolsAllowlistKey{}, runtimeToolsAllowlist{list: allowlist})
}

func RuntimeToolsAllowlistFromContext(ctx context.Context) ([]string, bool) {
	v, ok := ctx.Value(runtimeToolsAllowlistKey{}).(runtimeToolsAllowlist)
	if !ok {
		return nil, false
	}
	return v.list, true
}

type contextKey string

const (
	ContextKeyOutputByteLimit contextKey = "output_byte_limit"
	ContextKeyToolCallID      contextKey = "tool_call_id"
)

type edgeCountsKey struct{}

func WithEdgeCounts(ctx context.Context, counts map[string]int) context.Context {
	return context.WithValue(ctx, edgeCountsKey{}, counts)
}

func EdgeCountsFromContext(ctx context.Context) map[string]int {
	v, _ := ctx.Value(edgeCountsKey{}).(map[string]int)
	return v
}
