package taskengine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

type ToolDeclarations interface {
	GetToolsForToolsByName(context.Context, string) ([]Tool, error)
}

// DeclarationSchemas describes native tool inputs using the same declarations sent
// to models. Native operations have no HTTP endpoints, so live under components.
func DeclarationSchemas(ctx context.Context, group string, source ToolDeclarations) (map[string]*openapi3.T, error) {
	tools, err := source.GetToolsForToolsByName(ctx, group)
	if err != nil {
		return nil, err
	}
	doc := &openapi3.T{OpenAPI: "3.1.0", Info: &openapi3.Info{Title: group, Version: "1.0.0"}, Paths: openapi3.NewPaths(), Components: &openapi3.Components{Schemas: openapi3.Schemas{}}}
	for _, tool := range tools {
		fn := tool.Function
		if _, exists := doc.Components.Schemas[fn.Name]; exists {
			return nil, fmt.Errorf("%s: duplicate tool %s", group, fn.Name)
		}
		b, err := json.Marshal(fn.Parameters)
		if err != nil {
			return nil, fmt.Errorf("%s.%s parameters: %w", group, fn.Name, err)
		}
		var schema openapi3.Schema
		if err = json.Unmarshal(b, &schema); err != nil {
			return nil, fmt.Errorf("%s.%s schema: %w", group, fn.Name, err)
		}
		schema.Description = fn.Description
		doc.Components.Schemas[fn.Name] = &openapi3.SchemaRef{Value: &schema}
	}
	return map[string]*openapi3.T{group: doc}, nil
}
