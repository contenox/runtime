package localaiapi

import (
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFirstRunChatRecommendation(t *testing.T) {
	catalog, err := modelregistry.NewCurated().List(t.Context())
	require.NoError(t, err)
	hardware := modelhardware.Hardware{PreferredBackend: "llama", MemoryBytes: 5 << 30}
	require.Equal(t, "minicpm5-2b", recommendChat(catalog, hardware, nil, ""))
	installed := map[string]bool{"nanbeige4-3b-thinking": true}
	require.Equal(t, "nanbeige4-3b-thinking", recommendChat(catalog, hardware, installed, ""))
	require.Equal(t, "minicpm5-2b", recommendChat(catalog, hardware, installed, "nanbeige4-3b-thinking"))
	hardware.MemoryBytes = 300 << 20
	require.Empty(t, recommendChat(catalog, hardware, nil, ""))
}
