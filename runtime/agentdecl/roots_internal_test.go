package agentdecl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func rootOf(t *testing.T, dir string) Root {
	t.Helper()
	root, err := LocalRoot(dir)
	require.NoError(t, err)
	return root
}
