package localtools_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type specCaptureRunner struct{ spec *localtools.CommandSpec }

func (s *specCaptureRunner) Run(_ context.Context, spec localtools.CommandSpec, stdout, _ io.Writer) (int, error) {
	*s.spec = spec
	_, _ = io.WriteString(stdout, "ok")
	return 0, nil
}

func TestLocalExecSessionWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	absolute := t.TempDir()
	var spec localtools.CommandSpec
	tool := localtools.NewLocalExecToolsWith(&specCaptureRunner{spec: &spec},
		localtools.WithLocalExecCwdResolver(func(context.Context) string { return project }))
	for input, want := range map[string]string{"": project, ".": project, "sub": filepath.Join(project, "sub"), absolute: absolute} {
		_, _, err := tool.Exec(context.Background(), time.Now(), map[string]any{"command": "go", "cwd": input}, false, &taskengine.ToolsCall{})
		require.NoError(t, err)
		require.Equal(t, want, spec.Cwd)
	}
}

func TestUnit_LocalExecTools_WholeLineCommandIsSplitIntoProgramAndArgs(t *testing.T) {
	var spec localtools.CommandSpec
	h := localtools.NewLocalExecToolsWith(&specCaptureRunner{spec: &spec}).(*localtools.LocalExecTools)
	toolsCall := &taskengine.ToolsCall{Name: "local_shell", Args: map[string]string{"command": "go version -extra 'quoted arg'"}}

	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, "go", spec.Command)
	assert.Equal(t, []string{"version", "-extra", "quoted arg"}, spec.Args)
}

func TestUnit_LocalExecTools_WholeLineCommandKeptWhenProgramUnknown(t *testing.T) {
	var spec localtools.CommandSpec
	h := localtools.NewLocalExecToolsWith(&specCaptureRunner{spec: &spec}).(*localtools.LocalExecTools)
	toolsCall := &taskengine.ToolsCall{Name: "local_shell", Args: map[string]string{"command": "no-such-program-xyz --flag"}}

	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, "no-such-program-xyz --flag", spec.Command)
	assert.Empty(t, spec.Args)
}
