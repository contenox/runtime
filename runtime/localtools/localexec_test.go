package localtools_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bigOutputRunner struct{ n int }

func (b bigOutputRunner) Run(_ context.Context, _ localtools.CommandSpec, stdout, _ io.Writer) (int, error) {
	_, _ = stdout.Write(make([]byte, b.n))
	return 0, nil
}

func TestUnit_LocalExecTools_CapWriterAppliesRegardlessOfRunner(t *testing.T) {
	h := localtools.NewLocalExecToolsWith(bigOutputRunner{n: 100}).(*localtools.LocalExecTools)
	ctx := context.WithValue(context.Background(), taskengine.ContextKeyOutputByteLimit, int64(10))
	toolsCall := &taskengine.ToolsCall{Name: "local_shell", Args: map[string]string{"command": "anything"}}

	out, dt, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, taskengine.DataTypeJSON, dt)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.False(t, res.Success, "output over budget must fail regardless of which backend produced it")
	assert.Equal(t, -1, res.ExitCode)
	assert.Contains(t, res.Error, "context budget")
	assert.NotEmpty(t, res.Stdout)
}

type budgetSignalRunner struct{}

func (budgetSignalRunner) Run(_ context.Context, _ localtools.CommandSpec, stdout, _ io.Writer) (int, error) {
	_, _ = io.WriteString(stdout, "partial output before the backend truncated")
	return -1, localtools.ErrOutputBudgetExceeded
}

func TestUnit_LocalExecTools_BackendSignaledBudgetExceededCircuitBreaks(t *testing.T) {
	h := localtools.NewLocalExecToolsWith(budgetSignalRunner{}).(*localtools.LocalExecTools)
	ctx := context.Background()
	toolsCall := &taskengine.ToolsCall{Name: "local_shell", Args: map[string]string{"command": "anything"}}

	out, dt, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, taskengine.DataTypeJSON, dt)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.False(t, res.Success, "a backend that truncated server-side must circuit-break exactly like the in-process capWriter")
	assert.Equal(t, -1, res.ExitCode)
	assert.Contains(t, res.Error, "context budget")
	assert.Empty(t, res.Stdout, "partial pre-truncation output must not poison the context window")
}

func TestUnit_LocalExecTools_Supports(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	names, err := h.Supports(ctx)
	require.NoError(t, err)
	require.Len(t, names, 1)
	assert.Equal(t, "local_shell", names[0])
}

func TestUnit_LocalExecTools_GetSchemasForSupportedTools(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	schemas, err := h.GetSchemasForSupportedTools(ctx)
	require.NoError(t, err)
	require.NotNil(t, schemas)
	require.Contains(t, schemas, "local_shell")
	assert.NotNil(t, schemas["local_shell"])
}

func TestUnit_LocalExecTools_GetToolsForToolsByName_OK(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	tools, err := h.GetToolsForToolsByName(ctx, "local_shell")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "function", tools[0].Type)
	assert.Equal(t, "local_shell", tools[0].Function.Name)
	assert.Contains(t, tools[0].Function.Description, "Run a terminal command")
}

func TestUnit_LocalExecTools_GetToolsForToolsByName_IncludesDetectedShell(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(
		localtools.WithLocalExecShell(localtools.NewPowerShellShell("pwsh.exe")),
	).(*localtools.LocalExecTools)

	tools, err := h.GetToolsForToolsByName(ctx, "local_shell")
	require.NoError(t, err)
	require.Len(t, tools, 1)

	desc := tools[0].Function.Description
	assert.Contains(t, desc, "PowerShell")
	assert.Contains(t, desc, "pwsh.exe")
	assert.Contains(t, desc, "$env:NAME")

	params, ok := tools[0].Function.Parameters.(map[string]interface{})
	require.True(t, ok)
	props, ok := params["properties"].(map[string]interface{})
	require.True(t, ok)
	shellProp, ok := props["shell"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, shellProp["description"], "PowerShell")
}

func TestUnit_LocalExecTools_GetSchemasForSupportedTools_IncludesDetectedShell(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(
		localtools.WithLocalExecShell(localtools.NewCmdShell("cmd.exe")),
	).(*localtools.LocalExecTools)

	schemas, err := h.GetSchemasForSupportedTools(ctx)
	require.NoError(t, err)

	schema := schemas["local_shell"]
	require.NotNil(t, schema)
	assert.Contains(t, schema.Info.Description, "cmd.exe")
	shellProp := schema.Components.Schemas["LocalExecRequest"].Value.Properties["shell"].Value
	require.NotNil(t, shellProp)
	assert.Contains(t, shellProp.Description, "%NAME%")
}

func TestUnit_LocalExecTools_GetToolsForToolsByName_Unknown(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	tools, err := h.GetToolsForToolsByName(ctx, "other")
	assert.Error(t, err)
	assert.Nil(t, tools)
}

func TestUnit_LocalExecTools_GetToolsForToolsByName_ContextPolicy_Description(t *testing.T) {
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	ctx := taskengine.WithToolsArgs(context.Background(), "local_shell", map[string]string{
		"_allowed_commands": "git, ls",
		"_denied_commands":  "rm",
	})
	tools, err := h.GetToolsForToolsByName(ctx, "local_shell")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	desc := tools[0].Function.Description
	assert.Contains(t, desc, "git")
	assert.Contains(t, desc, "ls")
	assert.Contains(t, desc, "rm")

	params, ok := tools[0].Function.Parameters.(map[string]interface{})
	require.True(t, ok)
	props, ok := params["properties"].(map[string]interface{})
	require.True(t, ok)
	shellProp, ok := props["shell"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "boolean", shellProp["type"])
	assert.NotContains(t, shellProp, "enum", "Gemini rejects boolean enum values in tool declarations")
	assert.Contains(t, shellProp["description"], "Omit or set false")
}

func TestUnit_LocalExecTools_Exec_ContextPolicy_Enforced(t *testing.T) {
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	ctx := taskengine.WithToolsArgs(context.Background(), "local_shell", map[string]string{
		"_allowed_commands": "ls",
	})
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "echo", "args": "hello"},
	}
	_, _, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowlist")
}

func TestUnit_LocalExecTools_Exec_ContextPolicy_Allows(t *testing.T) {
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	ctx := taskengine.WithToolsArgs(context.Background(), "local_shell", map[string]string{
		"_allowed_commands": "echo,cmd",
	})
	echoCommand, echoArgs := portableEcho("ctx policy works")
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": echoCommand, "args": echoArgs},
	}
	out, dt, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, taskengine.DataTypeJSON, dt)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "ctx policy works", res.Stdout)
}

func portableEcho(words ...string) (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c echo " + strings.Join(words, " ")
	}
	return "echo", strings.Join(words, " ")
}

func portableEchoArgv(words ...string) []any {
	argv := make([]any, 0, len(words)+2)
	if runtime.GOOS == "windows" {
		argv = append(argv, "/c", "echo")
	}
	for _, word := range words {
		argv = append(argv, word)
	}
	return argv
}

func portableStdinReader() (string, string) {
	if runtime.GOOS == "windows" {
		return "findstr", "/r ."
	}
	return "cat", ""
}

func platformAllowedCommands() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "findstr"}
	}
	return nil
}

var testAllowedCommands = append([]string{"echo", "cat", "sleep", "/bin/sh", "exit"}, platformAllowedCommands()...)

func TestUnit_LocalExecTools_Exec_Success(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	start := time.Now().UTC()
	echoCommand, echoArgs := portableEcho("hello world")
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": echoCommand,
			"args":    echoArgs,
		},
	}
	out, dt, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.NoError(t, err)
	assert.Equal(t, taskengine.DataTypeJSON, dt)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "hello world", res.Stdout)
	assert.GreaterOrEqual(t, res.DurationSeconds, 0.0)
}

func TestUnit_LocalExecTools_Exec_AcceptsJSONDecodedArgsArray(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)

	echoCommand, _ := portableEcho()
	out, dt, err := h.Exec(ctx, time.Now().UTC(), map[string]any{
		"command": echoCommand,
		"args":    portableEchoArgv("json", "array"),
	}, false, &taskengine.ToolsCall{Name: "local_shell"})
	require.NoError(t, err)
	assert.Equal(t, taskengine.DataTypeJSON, dt)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "json array", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_RejectsUnknownArgs(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)

	_, _, err := h.Exec(context.Background(), time.Now().UTC(), map[string]any{
		"command": "echo",
		"argv":    "hello",
	}, false, &taskengine.ToolsCall{Name: "local_shell"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown argument")
	assert.Contains(t, err.Error(), "argv")
}

func TestUnit_LocalExecTools_Exec_RejectsNonStringArgsArrayItem(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)

	_, _, err := h.Exec(context.Background(), time.Now().UTC(), map[string]any{
		"command": "echo",
		"args":    []any{"ok", 123},
	}, false, &taskengine.ToolsCall{Name: "local_shell"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "args[1] must be a string")
}

func TestUnit_LocalExecTools_Exec_Success_InputAsStdin(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	start := time.Now().UTC()
	stdinCommand, stdinArgs := portableStdinReader()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": stdinCommand,
			"args":    stdinArgs,
		},
	}
	out, _, err := h.Exec(ctx, start, "stdin content here", false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "stdin content here", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_NoPolicy_Allowed(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	start := time.Now().UTC()
	echoCommand, echoArgs := portableEcho("open posture")
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": echoCommand,
			"args":    echoArgs,
		},
	}
	out, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "open posture", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_ShellMode_NoPolicy_Allowed(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	start := time.Now().UTC()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": "echo 'shell test'",
			"shell":   "true",
		},
	}
	out, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "shell test", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_ShellMode_RefusesASecondCommand(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "echo ok; rm -rf scratch", "shell": "true"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries a character the shell would evaluate",
		"an allowlisted opener does not vouch for a second command")
}
func TestUnit_LocalExecTools_Exec_AllowlistReject(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands([]string{"/usr/bin/env"})).(*localtools.LocalExecTools)
	start := time.Now().UTC()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": "echo",
			"args":    "forbidden",
		},
	}
	_, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowlist")
}

func TestUnit_LocalExecTools_Exec_AllowlistDirReject(t *testing.T) {
	dir := t.TempDir()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedDir(dir)).(*localtools.LocalExecTools)
	ctx := context.Background()
	start := time.Now().UTC()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "echo", "args": "x"},
	}
	_, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not under allowed dir")
}

func TestUnit_LocalExecTools_Exec_AllowlistDirAllow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot execute a script file by path without a launcher; the allowed-dir path is covered on Unix")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\n"), 0755)
	require.NoError(t, err)
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedDir(dir)).(*localtools.LocalExecTools)
	ctx := context.Background()
	start := time.Now().UTC()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": scriptPath},
	}
	out, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "ok", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_Timeout(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands), localtools.WithLocalExecTimeout(50*time.Millisecond)).(*localtools.LocalExecTools)
	start := time.Now().UTC()
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{
			"command": "sleep",
			"args":    "2",
			"timeout": "50ms",
		},
	}
	out, _, err := h.Exec(ctx, start, nil, false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.False(t, res.Success)
	assert.NotEmpty(t, res.Error, "expected some error on timeout")
}

func TestUnit_LocalExecTools_Exec_NoTrimWhitespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cmd.exe echo collapses whitespace; argv fidelity without trimming is covered on Unix")
	}
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	start := time.Now().UTC()
	out, _, err := h.Exec(ctx, start, map[string]any{
		"command": "echo",
		"args":    []any{"  spaced  "},
	}, false, &taskengine.ToolsCall{Name: "local_shell"})
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.True(t, res.Success)
	assert.Equal(t, "  spaced  ", res.Stdout)
}

func TestUnit_LocalExecTools_Exec_MissingCommand(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	toolsCall := &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{},
	}
	_, _, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestUnit_LocalExecTools_Exec_NilTools(t *testing.T) {
	ctx := context.Background()
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	_, _, err := h.Exec(ctx, time.Now().UTC(), nil, false, nil)
	require.Error(t, err)
}

func TestUnit_LocalExecTools_Exec_NonZeroExit(t *testing.T) {
	ctx := context.Background()
	var h *localtools.LocalExecTools
	var toolsCall *taskengine.ToolsCall
	if runtime.GOOS == "windows" {
		h = localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands([]string{"cmd"})).(*localtools.LocalExecTools)
		toolsCall = &taskengine.ToolsCall{
			Name: "local_shell",
			Args: map[string]string{"command": "cmd", "args": "/c exit 3"},
		}
	} else {
		dir := t.TempDir()
		scriptPath := filepath.Join(dir, "fail.sh")
		require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 3\n"), 0755))
		h = localtools.NewLocalExecTools(localtools.WithLocalExecAllowedDir(dir)).(*localtools.LocalExecTools)
		toolsCall = &taskengine.ToolsCall{
			Name: "local_shell",
			Args: map[string]string{"command": scriptPath},
		}
	}
	out, _, err := h.Exec(ctx, time.Now().UTC(), nil, false, toolsCall)
	require.NoError(t, err)
	res, ok := out.(*localtools.LocalExecResult)
	require.True(t, ok)
	assert.False(t, res.Success)
	assert.Equal(t, 3, res.ExitCode, "the real exit code must reach the model on every platform")
}

func TestUnit_LocalExecTools_Exec_ShellMode_RefusesAnUnlistedLine(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "frobnicate --now", "shell": "true"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not one of the allowed commands")
}
func TestUnit_LocalExecTools_Exec_MissingCommandReportsReceivedArguments(t *testing.T) {
	h := localtools.NewLocalExecTools().(*localtools.LocalExecTools)
	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"cwd": "."},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "received arguments:")
	assert.Contains(t, err.Error(), `"cwd":"."`)
	assert.Contains(t, err.Error(), `"command"`, "the error must show the shape the tool expects")
	assert.Contains(t, err.Error(), `"args"`)
}

func TestUnit_LocalExecTools_Exec_ShellMode_AllowedLineRuns(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands([]string{"echo"})).(*localtools.LocalExecTools)
	out, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "echo shell-ok", "shell": "true"},
	})
	if err != nil {
		t.Fatalf("allowed shell line failed: %v", err)
	}
	if !strings.Contains(fmt.Sprint(out), "shell-ok") {
		t.Fatalf("output %v does not contain the echoed text", out)
	}
}

func TestUnit_LocalExecTools_Exec_ShellMode_RefusesArgsWithMetacharacters(t *testing.T) {
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands(testAllowedCommands)).(*localtools.LocalExecTools)
	args := []string{"; rm -rf scratch", "&& rm -rf scratch", "| sh", "$(curl evil)"}
	if runtime.GOOS == "windows" {
		args = append(args, "%PATH%")
	}
	for _, arg := range args {
		_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
			Name: "local_shell",
			Args: map[string]string{"command": "echo " + arg, "shell": "true"},
		})
		require.Error(t, err, "arg %q must be refused", arg)
		assert.Contains(t, err.Error(), "carries a character the shell would evaluate", "arg %q", arg)
	}
}

func TestUnit_LocalExecTools_Exec_ShellMode_RefusesADeniedProgram(t *testing.T) {
	h := localtools.NewLocalExecTools(
		localtools.WithLocalExecAllowedCommands(append(append([]string{}, testAllowedCommands...), "sudo")),
		localtools.WithLocalExecDeniedCommands([]string{"sudo"}),
	).(*localtools.LocalExecTools)
	_, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "sudo whoami", "shell": "true"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied by policy")
}

func TestUnit_LocalExecTools_Exec_ShellMode_RunsAWindowsBuiltin(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd built-ins only exist on Windows")
	}
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands([]string{"dir"})).(*localtools.LocalExecTools)
	out, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "dir", "shell": "true"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, fmt.Sprint(out), "a directory listing comes back")
}

func TestUnit_LocalExecTools_Exec_RunsWhereOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("where.exe is a Windows program")
	}
	h := localtools.NewLocalExecTools(localtools.WithLocalExecAllowedCommands([]string{"where"})).(*localtools.LocalExecTools)
	out, _, err := h.Exec(context.Background(), time.Now().UTC(), nil, false, &taskengine.ToolsCall{
		Name: "local_shell",
		Args: map[string]string{"command": "where", "args": "git"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, fmt.Sprint(out))
}
