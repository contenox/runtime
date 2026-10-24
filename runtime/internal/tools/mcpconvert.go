package tools

import (
	"encoding/json"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

func mcpToolToTaskTool(toolsName string, t runtimetypes.MCPTool, injectParams map[string]string) taskengine.Tool {
	_ = toolsName
	var params any
	if len(t.InputSchema) > 0 {
		params = filterMCPSchema(t.InputSchema, injectParams)
	}
	return taskengine.Tool{
		Type: "function",
		Function: taskengine.FunctionTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		},
	}
}

func filterMCPSchema(rawSchema json.RawMessage, injectParams map[string]string) any {
	if len(injectParams) == 0 {
		var out any
		if err := json.Unmarshal(rawSchema, &out); err == nil {
			return out
		}
		return rawSchema
	}

	var schema map[string]any
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		var out any
		_ = json.Unmarshal(rawSchema, &out)
		return out
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		for k := range injectParams {
			delete(props, k)
		}
		schema["properties"] = props
	}

	if reqRaw, ok := schema["required"].([]any); ok {
		filtered := reqRaw[:0]
		for _, v := range reqRaw {
			if s, ok := v.(string); ok {
				if _, injected := injectParams[s]; !injected {
					filtered = append(filtered, v)
				}
			}
		}
		if len(filtered) > 0 {
			schema["required"] = filtered
		} else {
			delete(schema, "required")
		}
	}

	return schema
}
