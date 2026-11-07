package modelrepo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_UnavailableModelsAreHiddenUntilReset(t *testing.T) {
	models := []ObservedModel{{Name: "a"}, {Name: "b"}}
	require.Len(t, WithoutUnavailableModels("s1", models), 2)

	MarkModelUnavailable("s1", "a")
	got := WithoutUnavailableModels("s1", models)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].Name)
	require.Len(t, WithoutUnavailableModels("s2", models), 2, "other scopes are untouched")

	ResetUnavailableModels("s1")
	require.Len(t, WithoutUnavailableModels("s1", models), 2)
}

func TestUnit_UnavailableModelsListAndClearOneByOne(t *testing.T) {
	t.Cleanup(func() { ResetUnavailableModels("s3") })
	MarkModelUnavailable("s3", "b")
	MarkModelUnavailable("s3", "a")
	require.Equal(t, []string{"a", "b"}, UnavailableModels("s3"))

	MarkModelAvailable("s3", "a")
	require.Equal(t, []string{"b"}, UnavailableModels("s3"))
	MarkModelAvailable("s3", "missing")
	MarkModelAvailable("s3", "b")
	require.Empty(t, UnavailableModels("s3"))
}
