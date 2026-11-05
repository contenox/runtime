package contenoxcli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/locale"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func runConfigCommand(t *testing.T, dbPath string, sub *cobra.Command, args ...string) string {
	t.Helper()
	root := agentTestRoot(sub)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"--db", dbPath}, args...))
	require.NoError(t, root.Execute())
	return buf.String()
}

func TestUnit_ConfigLocale_GetPrintsDetectedThenOverride(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "config.db")
	get := &cobra.Command{Use: "get", Args: cobra.ExactArgs(1), RunE: configGetCmd.RunE}
	set := &cobra.Command{Use: "set", Args: cobra.ExactArgs(2), RunE: configSetCmd.RunE}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: configListCmd.RunE}

	require.Equal(t, locale.Detect()+"  (detected)\n", runConfigCommand(t, dbPath, get, "get", "locale"))

	require.Equal(t, "✓  locale = pt-BR  (global)\n", runConfigCommand(t, dbPath, set, "set", "locale", "pt_BR.UTF-8"))
	require.Equal(t, "pt-BR  (global)\n", runConfigCommand(t, dbPath, get, "get", "locale"))
	require.Contains(t, runConfigCommand(t, dbPath, list, "list"), "locale")

	require.Equal(t, "✓  locale =   (global)\n", runConfigCommand(t, dbPath, set, "set", "locale", ""))
	require.Equal(t, locale.Detect()+"  (detected)\n", runConfigCommand(t, dbPath, get, "get", "locale"))
}

func TestUnit_ConfigLocale_SetRejectsNonTags(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "config.db")
	set := &cobra.Command{Use: "set", Args: cobra.ExactArgs(2), RunE: configSetCmd.RunE}
	root := agentTestRoot(set)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--db", dbPath, "set", "locale", "German"})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "locale must be a language tag")
}

func TestUnit_BuildTemplateVars_LocaleAlwaysPresent(t *testing.T) {
	vars := buildTemplateVars(chatOpts{EffectiveLocale: "fr-CA"})
	require.Equal(t, "fr-CA", vars["locale"])

	vars = buildTemplateVars(chatOpts{})
	require.Equal(t, locale.Detect(), vars["locale"])
}
