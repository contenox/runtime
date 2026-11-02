package contenoxcli

import (
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/homedir"
	"github.com/stretchr/testify/require"
)

func TestUnit_ACPPolicyDirs_SearchTheDataFolderBeforeHome(t *testing.T) {
	isolateHome(t)
	data := t.TempDir()

	dirs := acpPolicyDirs(data)

	require.GreaterOrEqual(t, len(dirs), 2)
	require.Equal(t, filepath.Join(data, agentdecl.GeneratedDirName), dirs[0], "a unit resolves its mission envelope where serve rendered it")
	require.Equal(t, data, dirs[1])
}

func TestUnit_ACPPolicyDirs_DoesNotRepeatHome(t *testing.T) {
	isolateHome(t)
	home, err := homedir.Dir()
	require.NoError(t, err)

	dirs := acpPolicyDirs(home)
	require.Len(t, dirs, 2)

	seen := map[string]int{}
	for _, d := range dirs {
		seen[d]++
	}
	for d, n := range seen {
		require.Equal(t, 1, n, d)
	}
}
