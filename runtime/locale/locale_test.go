package locale

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Normalize(t *testing.T) {
	cases := map[string]string{
		"de_DE.UTF-8":     "de-DE",
		"de-DE":           "de-DE",
		"DE_de":           "de-DE",
		"en_US.utf8@euro": "en-US",
		"fr":              "fr",
		"zh-hant-tw":      "zh-Hant-TW",
		"es-419":          "es-419",
		"  pt_BR  ":       "pt-BR",
	}
	for raw, want := range cases {
		got, ok := Normalize(raw)
		require.True(t, ok, raw)
		require.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"", "C", "POSIX", "C.UTF-8", "de-", "english", "de-DEU", "x"} {
		_, ok := Normalize(raw)
		require.False(t, ok, raw)
	}
}

func TestUnit_Detect_PlatformFirstThenEnvThenFallback(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	platform := func(v string) func() string { return func() string { return v } }

	require.Equal(t, "de-DE", detect(env(map[string]string{"LANG": "fr_FR.UTF-8"}), platform("de-DE")))
	require.Equal(t, "fr-FR", detect(env(map[string]string{"LANG": "fr_FR.UTF-8"}), platform("")))
	require.Equal(t, "it-IT", detect(env(map[string]string{"LC_ALL": "it_IT", "LANG": "fr_FR.UTF-8"}), platform("")))
	require.Equal(t, "fr-FR", detect(env(map[string]string{"LC_ALL": "C", "LANG": "fr_FR.UTF-8"}), platform("")))
	require.Equal(t, Fallback, detect(env(map[string]string{"LANG": "C.UTF-8"}), platform("")))
	require.Equal(t, Fallback, detect(env(map[string]string{}), nil))
}

func TestUnit_Resolve_OverrideBeatsDetection(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "locale.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	require.Equal(t, Detect(), Resolve(ctx, store))
	require.Equal(t, "", Override(ctx, store))

	require.NoError(t, clikv.SetString(ctx, store, Key, "ja_JP.UTF-8"))
	require.Equal(t, "ja-JP", Override(ctx, store))
	require.Equal(t, "ja-JP", Resolve(ctx, store))

	require.NoError(t, clikv.SetString(ctx, store, Key, "not a locale"))
	require.Equal(t, Detect(), Resolve(ctx, store))

	require.NoError(t, clikv.SetString(ctx, store, Key, ""))
	require.Equal(t, Detect(), Resolve(ctx, store))
	require.Equal(t, Detect(), Resolve(ctx, nil))
}
