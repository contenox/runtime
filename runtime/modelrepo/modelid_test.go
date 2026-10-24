package modelrepo_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_ModelID_DropsTheResourcePrefixOnly(t *testing.T) {
	require.Equal(t, "gemini-2.5-flash", modelrepo.ModelID("models/gemini-2.5-flash"))
	require.Equal(t, "gemini-2.5-flash", modelrepo.ModelID(" gemini-2.5-flash "))
	require.Equal(t, "org/model:tag", modelrepo.ModelID("org/model:tag"))
	require.Equal(t, "tunedModels/x", modelrepo.ModelID("tunedModels/x"))
}

func TestUnit_SameModel_TreatsTheResourceNameAndTheBareIDAsOneModel(t *testing.T) {
	require.True(t, modelrepo.SameModel("models/gemini-2.5-flash", "gemini-2.5-flash"))
	require.True(t, modelrepo.SameModel("gemini-2.5-flash", "models/gemini-2.5-flash"))
	require.True(t, modelrepo.SameModel("gemini-2.5-flash", "gemini-2.5-flash"))
	require.False(t, modelrepo.SameModel("models/gemini-2.5-flash", "gemini-2.5-pro"))
	require.False(t, modelrepo.SameModel("Gemini-2.5-Flash", "gemini-2.5-flash"), "ids stay case sensitive")
}
