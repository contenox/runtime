package hooks

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/contenox/runtime/taskengine"
)

// SimpleRepo holds a map of locally registered hooks.
type SimpleRepo struct {
	hooks map[string]taskengine.HookRepo
}

func NewSimpleProvider(hooks map[string]taskengine.HookRepo) taskengine.HookRepo {
	return &SimpleRepo{
		hooks: hooks,
	}
}

// Exec finds the correct hook by name from its internal map and delegates the execution.
func (m *SimpleRepo) Exec(
	ctx context.Context,
	startingTime time.Time,
	input any,
	args *taskengine.HookCall,
) (any, error) {
	if hook, ok := m.hooks[args.Name]; ok {
		return hook.Exec(ctx, startingTime, input, args)
	}
	return nil, fmt.Errorf("unknown hook type: %s", args.Name)
}

// Supports returns a list of all hook names registered in the internal map.
func (m *SimpleRepo) Supports(ctx context.Context) ([]string, error) {
	supported := make([]string, 0, len(m.hooks))
	for k := range m.hooks {
		supported = append(supported, k)
	}
	return supported, nil
}

// GetSchemasForSupportedHooks aggregates the schemas from all registered hooks.
func (m *SimpleRepo) GetSchemasForSupportedHooks(ctx context.Context) (map[string]map[string]interface{}, error) {
	allSchemas := make(map[string]map[string]interface{})

	// Iterate through each registered hook implementation.
	for hookName, hookImpl := range m.hooks {
		// Get the schemas provided by this specific hook's implementation.
		implSchemas, err := hookImpl.GetSchemasForSupportedHooks(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting schema for hook '%s': %w", hookName, err)
		}

		// Merge the returned schemas into our main map.
		maps.Copy(allSchemas, implSchemas)
	}
	return allSchemas, nil
}

func (m *SimpleRepo) GetToolsForHookByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	if hook, ok := m.hooks[name]; ok {
		return hook.GetToolsForHookByName(ctx, name)
	}
	return nil, fmt.Errorf("unknown hook type: %s", name)
}

var _ taskengine.HookRepo = (*SimpleRepo)(nil)
