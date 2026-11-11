package localfileapi

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_FailureMessage_NamesNoPathOnDisk(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	abs := filepath.Join(root, "rp", "two.txt")
	pathErr := &fs.PathError{Op: "read", Path: abs, Err: errors.New("incorrect function")}
	require.Equal(t, "incorrect function", failureMessage(root, pathErr))
	require.Equal(t, "invalid path: "+filepath.Join("rp", "two.txt")+" is locked", failureMessage(root, fmt.Errorf("invalid path: %s is locked", abs)))
}
