package externalagents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_NodePlatformNames(t *testing.T) {
	require.Equal(t, "win32", nodePlatform("windows"))
	require.Equal(t, "linux", nodePlatform("linux"))
	require.Equal(t, "darwin", nodePlatform("darwin"))
	require.Equal(t, "x64", nodeArch("amd64"))
	require.Equal(t, "arm64", nodeArch("arm64"))
}
