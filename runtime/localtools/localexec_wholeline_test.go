package localtools_test

import (
	"context"
	"io"
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
