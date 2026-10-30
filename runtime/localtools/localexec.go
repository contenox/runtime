package localtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
)

const localExecToolsName = "local_shell"

// LocalExecToolsName is the registered toolset name local_shell answers on.
const LocalExecToolsName = localExecToolsName

type LocalExecResult struct {
	ExitCode        int     `json:"exit_code"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	Success         bool    `json:"success"`
	Error           string  `json:"error,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
	Command         string  `json:"command,omitempty"`
	Shell           string  `json:"shell,omitempty"`
	OS              string  `json:"os,omitempty"`
}

type LocalExecTools struct {
	defaultTimeout  time.Duration
	allowedDir      string
	allowedCommands []string
	deniedCommands  []string
	runner          CommandRunner
	shell           PlatformShell
	scrubEnv        func([]string) []string
}

type LocalExecOption func(*LocalExecTools)

func WithLocalExecTimeout(d time.Duration) LocalExecOption {
	return func(h *LocalExecTools) {
		h.defaultTimeout = d
	}
}

func WithLocalExecAllowedDir(dir string) LocalExecOption {
	return func(h *LocalExecTools) {
		h.allowedDir = filepath.Clean(dir)
	}
}

func WithLocalExecAllowedCommands(commands []string) LocalExecOption {
	return func(h *LocalExecTools) {
		h.allowedCommands = commands
	}
}

func WithLocalExecDeniedCommands(commands []string) LocalExecOption {
	return func(h *LocalExecTools) {
		h.deniedCommands = commands
	}
}

func WithLocalExecShell(shell PlatformShell) LocalExecOption {
	return func(h *LocalExecTools) {
		h.shell = shell.WithDefaults()
	}
}

func WithLocalExecScrubEnv(scrub func([]string) []string) LocalExecOption {
	return func(h *LocalExecTools) {
		h.scrubEnv = scrub
	}
}

func NewLocalExecTools(opts ...LocalExecOption) taskengine.ToolsRepo {
	return NewLocalExecToolsWith(nil, opts...)
}

func NewLocalExecToolsWith(runner CommandRunner, opts ...LocalExecOption) taskengine.ToolsRepo {
	h := &LocalExecTools{
		defaultTimeout: 60 * time.Second,
		shell:          DetectPlatformShell(),
	}
	for _, opt := range opts {
		opt(h)
	}
	if runner == nil {
		runner = NewOSCommandRunnerWithShellAndScrub(h.shell, h.scrubEnv)
	}
	h.runner = runner
	return h
}

func (h *LocalExecTools) resolvePolicy(ctx context.Context) (allowedCommands []string, allowedDir string, deniedCommands []string) {
	if args := taskengine.ToolsArgsFromContext(ctx, localExecToolsName); len(args) > 0 {
		if v := args["_allowed_commands"]; v != "" {
			allowedCommands = splitTrimmed(v)
		}
		if v := args["_allowed_dir"]; v != "" {
			allowedDir = filepath.Clean(v)
		}
		if v := args["_denied_commands"]; v != "" {
			deniedCommands = splitTrimmed(v)
		}
		return
	}
	return h.allowedCommands, h.allowedDir, h.deniedCommands
}

func splitTrimmed(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (h *LocalExecTools) Exec(ctx context.Context, startTime time.Time, input any, debug bool, tools *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if tools == nil {
		return nil, taskengine.DataTypeAny, errors.New("local_shell: tools required")
	}
	if tools.Args == nil {
		tools.Args = make(map[string]string)
	}
	command, argsSlice, cwd, timeout, useShell, stdin, err := h.parseArgs(ctx, tools, input)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	allowedCommands, allowedDir, deniedCommands := h.resolvePolicy(ctx)
	if err := h.checkAllowlist(command, argsSlice, useShell, allowedCommands, allowedDir, deniedCommands); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	result, err := h.run(ctx, command, argsSlice, cwd, timeout, useShell, stdin)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	return result, taskengine.DataTypeJSON, nil
}

func (h *LocalExecTools) parseArgs(ctx context.Context, tools *taskengine.ToolsCall, input any) (command string, argsSlice []string, cwd string, timeout time.Duration, useShell bool, stdin string, err error) {
	timeout = h.defaultTimeout
	get := func(k string) string { return tools.Args[k] }
	if cmd := get("command"); cmd != "" {
		command = cmd
	}
	if a := get("args"); a != "" {
		argsSlice = splitShellArgs(a)
	}
	if d := get("cwd"); d != "" {
		cwd = filepath.Clean(d)
		if absCwd, err := filepath.Abs(cwd); err == nil {
			cwd = absCwd
		}
	}
	if t := get("timeout"); t != "" {
		if d, e := time.ParseDuration(t); e == nil {
			timeout = d
		}
	}
	if s := get("shell"); s != "" {
		useShell = strings.EqualFold(s, "true") || s == "1"
	}
	switch v := input.(type) {
	case string:
		stdin = v
		if command == "" {
			command = v
			if useShell {
				argsSlice = nil
			}
		}
	case map[string]any:
		if err := rejectUnknownArgs(localExecToolsName, v, "command", "args", "cwd", "timeout", "shell", "stdin"); err != nil {
			return "", nil, "", 0, false, "", err
		}
		if cmd, ok := v["command"].(string); ok && command == "" {
			command = cmd
		}
		if s, ok := v["stdin"].(string); ok {
			stdin = s
		}
		if s, ok := v["shell"].(bool); ok && !useShell {
			useShell = s
		} else if s, ok := v["shell"].(string); ok && !useShell {
			useShell = strings.EqualFold(s, "true") || s == "1"
		}
		if a, ok := v["args"]; ok && len(argsSlice) == 0 {
			parsed, err := stringSliceArg(localExecToolsName, "args", a)
			if err != nil {
				return "", nil, "", 0, false, "", err
			}
			argsSlice = parsed
		}
		if d, ok := v["cwd"].(string); ok && cwd == "" {
			cwd = filepath.Clean(d)
			if absCwd, err := filepath.Abs(cwd); err == nil {
				cwd = absCwd
			}
		}
		if t, ok := v["timeout"].(string); ok {
			if d, e := time.ParseDuration(t); e == nil && timeout == h.defaultTimeout {
				timeout = d
			}
		}
	}
	if command == "" {
		message := `local_shell: command is required; send either {"command": "git"} or {"command": "git", "args": ["status", "--short"]} (a plain input string is also accepted as the command)`
		return "", nil, "", 0, false, "", argumentProblem(ctx, message, receivedShellArguments(tools, input))
	}
	return command, argsSlice, cwd, timeout, useShell, stdin, nil
}

func receivedShellArguments(tools *taskengine.ToolsCall, input any) map[string]any {
	received := make(map[string]any)
	if tools != nil {
		for key, value := range tools.Args {
			if value != "" {
				received[key] = value
			}
		}
	}
	switch value := input.(type) {
	case string:
		if value != "" {
			received["input"] = value
		}
	case map[string]any:
		for key, item := range value {
			received[key] = item
		}
	}
	return received
}

func (h *LocalExecTools) checkAllowlist(command string, argsSlice []string, useShell bool, allowedCommands []string, allowedDir string, deniedCommands []string) error {
	if useShell && (len(allowedCommands) > 0 || len(deniedCommands) > 0 || allowedDir != "") {
		// Check the line the shell will actually run — command plus argv — not
		// just the program name, or an argument could carry a second command.
		script := strings.TrimSpace(command + " " + strings.Join(argsSlice, " "))
		if len(allowedCommands) == 0 {
			return fmt.Errorf("local_shell: 'shell: true' needs _allowed_commands; name the programs the line may start with")
		}
		if !hitlservice.CommandLineAllowedByPrefix(script, strings.Join(allowedCommands, ",")) {
			return fmt.Errorf("local_shell: %q is not one of the allowed commands, or it carries a character the shell would evaluate; run it without shell:true or name it in _allowed_commands", script)
		}
		// The gate refuses every metacharacter, so the line holds one program:
		// the denylist and the directory rule apply to it below.
		if fields := strings.Fields(script); len(fields) > 0 {
			command = fields[0]
		}
	}

	resolved := command
	if !filepath.IsAbs(command) {
		if path, err := exec.LookPath(command); err == nil {
			resolved = path
		} else {
			resolved = filepath.Clean(command)
		}
	} else {
		resolved = filepath.Clean(command)
	}
	if len(deniedCommands) > 0 {
		base := filepath.Base(resolved)
		for _, d := range deniedCommands {
			dClean := filepath.Clean(d)
			if dClean == resolved || dClean == command || filepath.Base(dClean) == base || dClean == base {
				return fmt.Errorf("local_shell: command %s is denied by policy", command)
			}
		}
	}
	if allowedDir != "" {
		absDir, err := filepath.Abs(allowedDir)
		if err != nil {
			return fmt.Errorf("local_shell: allowed dir invalid: %w", err)
		}
		absCmd, err := filepath.Abs(resolved)
		if err != nil {
			return fmt.Errorf("local_shell: command path invalid: %w", err)
		}
		rel, err := filepath.Rel(absDir, absCmd)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("local_shell: command %s is not under allowed dir %s", command, allowedDir)
		}
	}
	if len(allowedCommands) > 0 {
		allowed := false
		for _, c := range allowedCommands {
			cClean := filepath.Clean(c)
			if cClean == resolved || cClean == command {
				allowed = true
				break
			}
			if path, err := exec.LookPath(c); err == nil && path == resolved {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("local_shell: command %s is not in allowlist", command)
		}
	}
	return nil
}

type capWriter struct {
	buf       bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (cw *capWriter) Write(p []byte) (n int, err error) {
	if cw.limit > 0 {
		if cw.written >= cw.limit {
			cw.truncated = true
			return 0, io.ErrShortWrite
		}
		writeLen := int64(len(p))
		if cw.written+writeLen > cw.limit {
			writeLen = cw.limit - cw.written
			cw.buf.Write(p[:writeLen])
			cw.written += writeLen
			cw.truncated = true
			return int(writeLen), io.ErrShortWrite
		}
	}
	n, err = cw.buf.Write(p)
	cw.written += int64(n)
	return n, err
}

func (h *LocalExecTools) run(ctx context.Context, command string, argsSlice []string, cwd string, timeout time.Duration, useShell bool, stdinStr string) (*LocalExecResult, error) {
	start := time.Now()
	shell := h.shell.WithDefaults()
	result := &LocalExecResult{Command: command, Shell: shell.Summary(), OS: shell.OS}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := int64(2 * 1024 * 1024)
	if val, ok := ctx.Value(taskengine.ContextKeyOutputByteLimit).(int64); ok {
		limit = val
	}

	stdout := newSpoolWriter(ctx, "local_shell-stdout", limit)
	stderr := newSpoolWriter(ctx, "local_shell-stderr", limit)

	spec := CommandSpec{
		Command:  command,
		Args:     argsSlice,
		Cwd:      cwd,
		Timeout:  timeout,
		UseShell: useShell,
		Shell:    shell,
		Stdin:    stdinStr,
	}
	exitCode, runErr := h.runner.Run(runCtx, spec, stdout, stderr)
	result.DurationSeconds = time.Since(start).Seconds()

	if errors.Is(runErr, ErrOutputBudgetExceeded) {
		stdout.discard()
		stderr.discard()
		result.Success = false
		result.ExitCode = -1
		result.Error = fmt.Sprintf("Output truncated: command exceeded the context budget (%d bytes). Re-run with a narrower scope or redirect output to a file. %s", limit, severityRecoverable)
		return result, nil
	}

	stdoutPath := stdout.close()
	stderrPath := stderr.close()
	result.Stdout = normalizeShellOutput(stdout.inlineOutput())
	result.Stderr = normalizeShellOutput(stderr.inlineOutput())

	if stdout.truncated() || stderr.truncated() {
		result.Success = false
		result.ExitCode = -1
		var parts []string
		if stdout.truncated() {
			parts = append(parts, spoolNotice("stdout", stdout, stdoutPath, limit))
		}
		if stderr.truncated() {
			parts = append(parts, spoolNotice("stderr", stderr, stderrPath, limit))
		}
		result.Error = strings.Join(parts, " ") + " " + truncationSeverity(stdout, stderr)
		return result, nil
	}

	if runErr != nil {
		result.Error = runErr.Error() + " " + severityRecoverable
		result.Success = false
		result.ExitCode = exitCode
		return result, nil
	}
	result.ExitCode = exitCode
	result.Success = exitCode == 0
	if !result.Success && result.Error == "" {
		result.Error = fmt.Sprintf("command exited with status %d %s", exitCode, severityRecoverable)
	}
	return result, nil
}

func normalizeShellOutput(output string) string {
	return strings.TrimRight(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
}

func spoolNotice(stream string, w *spoolWriter, path string, budget int64) string {
	if w.spoolErr != nil {
		return fmt.Sprintf("Output truncated: %s exceeded the context budget (%d bytes); the full output could not be spooled (%v).", stream, budget, w.spoolErr)
	}
	if w.overCap {
		return fmt.Sprintf("Output truncated: %s exceeded the context budget (%d bytes); showing the first 20%% and last 80%%; full output (first %s of a larger stream): %s.", stream, budget, humanSize(w.spooled), path)
	}
	return fmt.Sprintf("Output truncated: %s exceeded the context budget (%d bytes); showing the first 20%% and last 80%%; full output: %s (%s).", stream, budget, path, humanSize(w.total))
}

func truncationSeverity(stdout, stderr *spoolWriter) string {
	if stdout.spoolErr != nil || stderr.spoolErr != nil {
		reason := "spool unwritable"
		if isDiskFull(stdout.spoolErr) || isDiskFull(stderr.spoolErr) {
			reason = "disk full"
		}
		return "(fatal: " + reason + ")"
	}
	return severityRecoverable
}

func splitShellArgs(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func (h *LocalExecTools) Supports(ctx context.Context) ([]string, error) {
	return []string{localExecToolsName}, nil
}

func (h *LocalExecTools) GetSchemasForSupportedTools(ctx context.Context) (map[string]*openapi3.T, error) {
	shellDesc := h.shell.ShellModeDescription()
	schema := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "Local Exec Tools", Description: "Run commands on the local host. " + shellDesc, Version: "1.0.0"},
		Paths:   openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"LocalExecRequest": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{openapi3.TypeObject},
						Properties: map[string]*openapi3.SchemaRef{
							"command": {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Description: "Executable path or name"}},
							"args": {Value: &openapi3.Schema{
								OneOf: []*openapi3.SchemaRef{
									{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Description: "Space-separated arguments string"}},
									{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeArray}, Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}}, Description: "Array of argument strings"}},
								},
								Description: "Arguments as space-separated string or array of strings",
							}},
							"cwd":     {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Description: "Working directory"}},
							"timeout": {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Description: "Duration e.g. 30s"}},
							"shell":   {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeBoolean}, Description: shellDesc}},
						},
						Required: []string{"command"},
					},
				},
				"LocalExecResponse": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{openapi3.TypeObject},
						Properties: map[string]*openapi3.SchemaRef{
							"exit_code":        {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}}},
							"stdout":           {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
							"stderr":           {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
							"success":          {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeBoolean}}},
							"error":            {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
							"duration_seconds": {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeNumber}}},
							"command":          {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
							"shell":            {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
							"os":               {Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
						},
					},
				},
			},
		},
	}
	return map[string]*openapi3.T{localExecToolsName: schema}, nil
}

func (h *LocalExecTools) GetToolsForToolsByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	if name != localExecToolsName {
		return nil, fmt.Errorf("unknown tools: %s", name)
	}
	allowedCommands, allowedDir, deniedCommands := h.resolvePolicy(ctx)
	shellDesc := h.shell.ShellModeDescription()
	desc := "Run a terminal command on the local host. Returns {stdout, stderr, exitCode, success, durationSeconds}. Output is capped at the remaining context budget; when it exceeds the cap the FULL output is spooled to a file and stdout/stderr instead carry a 20%-head/80%-tail slice (errors cluster at the tail) with an inline pointer, while the error field names the spool concretely ('full output: <path> (N KiB)') so nothing is lost silently. Errors carry a severity marker you can key on: '(recoverable: adjust parameters and retry)' for anything a corrected call fixes (output too large, bad command, denied path), and '(fatal: <reason>)' only when the environment is broken (disk full, spool unwritable) and retrying will not help. For file operations prefer local_fs.* tools: read_file, write_file, sed, find_files. They enforce sandbox boundaries, size limits, and a read-before-write contract that local_shell does not. Use local_shell for operations with no dedicated tool: running tests, builds, git commands, environment inspection. " + shellDesc
	if len(allowedCommands) > 0 {
		desc += " Allowed commands: " + strings.Join(allowedCommands, ", ") + "."
	}
	if allowedDir != "" {
		desc += " Commands must reside under: " + allowedDir + "."
	}
	if len(deniedCommands) > 0 {
		desc += " Denied commands: " + strings.Join(deniedCommands, ", ") + "."
	}

	policyActive := len(allowedCommands) > 0 || allowedDir != "" || len(deniedCommands) > 0
	var shellProp map[string]interface{}
	if policyActive {
		shellProp = map[string]interface{}{
			"type":        "boolean",
			"description": "Shell mode is disabled by the active command policy. Omit or set false; provide command and args as separate parameters.",
		}
	} else {
		shellProp = map[string]interface{}{
			"type":        "boolean",
			"description": shellDesc,
		}
	}

	return []taskengine.Tool{
		{
			Type: "function",
			Function: taskengine.FunctionTool{
				Name:        "local_shell",
				Description: desc,
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Executable path or name (required)",
						},
						"args": map[string]interface{}{
							"oneOf": []interface{}{
								map[string]interface{}{
									"type":        "string",
									"description": "Space-separated arguments string",
								},
								map[string]interface{}{
									"type":        "array",
									"items":       map[string]interface{}{"type": "string"},
									"description": "Array of argument strings",
								},
							},
							"description": "Arguments as space-separated string or array of strings",
						},
						"cwd": map[string]interface{}{
							"type":        "string",
							"description": "Working directory",
						},
						"timeout": map[string]interface{}{
							"type":        "string",
							"description": "Duration e.g. 30s",
						},
						"shell": shellProp,
					},
					"required": []string{"command"},
				},
			},
		},
	}, nil
}

var _ taskengine.ToolsRepo = (*LocalExecTools)(nil)
