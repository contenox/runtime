package localtools_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/eventtools"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/mediageneration"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestNativeDiscoveryMatchesModelDeclarations(t *testing.T) {
	ctx := t.Context()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "discovery.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	project := localtools.NewLocalFSToolsWith(t.TempDir(), db, nil, "project_docs", nil, localtools.WithMediaRenderer(mediageneration.Render))
	store := runtimetypes.New(db.WithoutTransaction())
	for role, name := range map[string]string{transport.ImageRole: "generate_image", transport.VideoRole: "generate_video"} {
		_, err := project.GetToolsForToolsByName(ctx, name)
		require.Error(t, err)
		require.NoError(t, store.SetKV(ctx, clikv.Prefix+clikv.EnabledRoleKey(role), json.RawMessage(`"enabled"`)))
		tools, err := project.GetToolsForToolsByName(ctx, name)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		require.Equal(t, name, tools[0].Function.Name)
		params := tools[0].Function.Parameters.(map[string]any)
		properties := params["properties"].(map[string]any)
		require.Contains(t, properties, "width")
		require.Contains(t, properties, "height")
		require.NotContains(t, properties, "model")
		if role == transport.VideoRole {
			require.Contains(t, properties, "fps")
			require.Contains(t, properties, "seconds")
		} else {
			require.NotContains(t, properties, "fps")
			require.NotContains(t, properties, "seconds")
		}
	}
	for group, provider := range map[string]taskengine.ToolsRepo{
		"project_docs": project,
		"events":       &eventtools.Provider{},
	} {
		docs, err := provider.GetSchemasForSupportedTools(ctx)
		require.NoError(t, err)
		doc := docs[group]
		require.NotNil(t, doc)
		require.NoError(t, doc.Validate(ctx))
		tools, err := provider.GetToolsForToolsByName(ctx, group)
		require.NoError(t, err)
		require.Len(t, doc.Components.Schemas, len(tools))
		for _, tool := range tools {
			schema := doc.Components.Schemas[tool.Function.Name]
			require.NotNil(t, schema)
			b, err := json.Marshal(schema.Value)
			require.NoError(t, err)
			var params map[string]any
			require.NoError(t, json.Unmarshal(b, &params))
			require.Equal(t, tool.Function.Description, params["description"])
			delete(params, "description")
			if params["properties"] == nil {
				params["properties"] = map[string]any{}
			}
			want, err := json.Marshal(tool.Function.Parameters)
			require.NoError(t, err)
			got, err := json.Marshal(params)
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(got))
		}
	}
}

func TestMediaToolRejectsDayLongVideoBeforeDispatchToModeld(t *testing.T) {
	dir := t.TempDir()
	project := localtools.NewLocalFSToolsWith(dir, nil, nil, "local_fs", nil, localtools.WithMediaRenderer(mediageneration.Render))
	_, err := execTool(t, t.Context(), project, "generate_video", map[string]any{
		"prompt": "lake", "width": "256", "height": "160", "fps": "7", "seconds": "86400",
	})
	require.ErrorContains(t, err, "render budget")
	require.NoDirExists(t, filepath.Join(dir, "Media"))
}
