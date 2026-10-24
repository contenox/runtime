package vertex

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func functionSchemaOf(t *testing.T, parameters any) map[string]any {
	t.Helper()

	req, err := buildVertexRequest("gemini-2.5-pro", []modelrepo.Message{{Role: "user", Content: "hi"}},
		[]modelrepo.ChatArgument{modelrepo.WithTools(modelrepo.Tool{
			Type: "function",
			Function: &modelrepo.FunctionTool{
				Name:        "git.paths",
				Description: "List repository paths.",
				Parameters:  parameters,
			},
		})})
	require.NoError(t, err)
	require.Len(t, req.Tools, 1)
	require.Len(t, req.Tools[0].FunctionDeclarations, 1)

	raw := req.Tools[0].FunctionDeclarations[0].ParametersJsonSchema
	require.NotEmpty(t, raw, "a declared schema goes out on the JSON Schema member")
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	return schema
}

func TestUnit_BuildVertexRequest_UnionWithItemsIsSentWhole(t *testing.T) {
	t.Parallel()

	schema := functionSchemaOf(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"paths": map[string]any{
				"type":        []any{"string", "array"},
				"items":       map[string]any{"type": "string"},
				"description": "A path relative to the repository root, or an array of them.",
			},
		},
		"required": []string{"paths"},
	})

	paths, ok := schema["properties"].(map[string]any)["paths"].(map[string]any)
	require.True(t, ok, "paths property survives")
	require.Equal(t, []any{"string", "array"}, paths["type"], "the union is not collapsed to one branch")
	require.NotNil(t, paths["items"], "the array branch keeps its items")
}

func TestUnit_BuildVertexRequest_UnionWithoutItemsIsSentWhole(t *testing.T) {
	t.Parallel()

	schema := functionSchemaOf(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"body": map[string]any{
				"type":        []any{"string", "number", "integer", "boolean", "object", "array", "null"},
				"description": "Request body.",
			},
		},
	})

	body, ok := schema["properties"].(map[string]any)["body"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"string", "number", "integer", "boolean", "object", "array", "null"}, body["type"])
	require.Nil(t, body["items"])
}

func TestUnit_BuildVertexRequest_ScalarItemsIsSentWhole(t *testing.T) {
	t.Parallel()

	schema := functionSchemaOf(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":  "string",
				"items": map[string]any{"type": "string"},
			},
		},
	})

	name, ok := schema["properties"].(map[string]any)["name"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", name["type"])
	require.NotNil(t, name["items"])
}

func TestUnit_BuildVertexRequest_PlainArrayKeepsItems(t *testing.T) {
	t.Parallel()

	schema := functionSchemaOf(t, map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	})
	require.Equal(t, "array", schema["type"])
	items, ok := schema["items"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", items["type"])
}

func TestUnit_BuildVertexRequest_SchemaMembersAreNotTrimmed(t *testing.T) {
	t.Parallel()

	schema := functionSchemaOf(t, map[string]any{
		"$defs": map[string]any{"Path": map[string]any{"type": "string"}},
		"type":  "object",
		"properties": map[string]any{
			"path": map[string]any{
				"$ref":                 "#/$defs/Path",
				"description":          "Repository path.",
				"enum":                 []any{"a", "b"},
				"additionalProperties": false,
			},
		},
		"additionalProperties": false,
	})

	require.Equal(t, false, schema["additionalProperties"])
	require.NotNil(t, schema["$defs"])
	path, ok := schema["properties"].(map[string]any)["path"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "#/$defs/Path", path["$ref"])
	require.Equal(t, []any{"a", "b"}, path["enum"])
	require.Equal(t, false, path["additionalProperties"])
}
