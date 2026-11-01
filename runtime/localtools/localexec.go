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

	"github.com/contenox/runtime/libtracker"
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

type LocalExecGate int

const (
	GateAllowlist LocalExecGate = iota
	GatePolicy
	GateRequireAllowlist
)

type LocalExecTools struct {
	gate            LocalExecGate
	defaultTimeout  time.Duration
	allowedDir      string
	allowedCommands []string
	deniedCommands  []string
	toolDirs        []string
	runner          CommandRunner
	shell           PlatformShell
	scrubEnv        func([]string) []string
	tracker         libtracker.ActivityTracker
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

func WithLocalExecGate(gate LocalExecGate) LocalExecOption {
	return func(h *LocalExecTools) { h.gate = gate }
}

func WithLocalExecTracker(tracker libtracker.ActivityTracker) LocalExecOption {
	return func(h *LocalExecTools) {
		if tracker != nil {
			h.tracker = tracker
		}
	}
}

func WithLocalExecToolDirs(dirs []string) LocalExecOption {
	return func(h *LocalExecTools) {
		h.toolDirs = append(h.toolDirs, dirs...)
	}
}

func NewLocalExecTools(opts ...LocalExecOption) taskengine.ToolsRepo {
	return NewLocalExecToolsWith(nil, opts...)
}

func NewLocalExecToolsWith(runner CommandRunner, opts ...LocalExecOption) taskengine.ToolsRepo {
	h := &LocalExecTools{
		defaultTimeout: 60 * time.Second,
		shell:          DetectPlatformShell(),
		tracker:        libtracker.NoopTracker{},
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

func (h *LocalExecTools) resolvePolicy(ctx context.Context) (allowedCommands []string, allowedDir string, deniedCommands []string, toolDirs []string) {
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
		if v := args["_allowed_tool_dirs"]; v != "" {
			toolDirs = splitToolDirs(v)
		}
		return allowedCommands, allowedDir, deniedCommands, toolDirs
	}
	return h.allowedCommands, h.allowedDir, h.deniedCommands, h.toolDirs
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
	allowedCommands, allowedDir, deniedCommands, toolDirs := h.resolvePolicy(ctx)
	reportErr, reportChange, end := h.tracker.Start(ctx, "local_shell", "exec",
		"command", command, "cwd", cwd, "shell", useShell)
	defer end()

	resolution, refusalErr := h.checkAllowlist(command, argsSlice, useShell, allowedCommands, allowedDir, deniedCommands, toolDirs)
	if resolution.LookupErr != nil {
		logUnresolved(ctx, resolution, !useShell)
		reportChange("unresolved", map[string]any{
			"command": resolution.Command,
			"error":   resolution.LookupErr.Error(),
		})
	}
	if refusalErr != nil {
		if refusal := asPolicyRefusal(refusalErr); refusal != nil {
			logRefusal(ctx, refusal)
			reportChange("refused", refusal.Telemetry())
		}
		reportErr(refusalErr)
		return nil, taskengine.DataTypeAny, refusalErr
	}
	result, runErr := h.run(ctx, command, argsSlice, cwd, timeout, useShell, stdin)
	if runErr != nil {
		reportErr(runErr)
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
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return "", nil, "", 0, false, "", fmt.Errorf("local_shell: cwd %q cannot be made absolute: %w", d, err)
		}
		cwd = absCwd
	}
	if t := get("timeout"); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return "", nil, "", 0, false, "", recoverablef("local_shell: timeout %q is not a duration (write it as 30s, 2m, 1h): %v", t, err)
		}
		timeout = d
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
			absCwd, err := filepath.Abs(cwd)
			if err != nil {
				return "", nil, "", 0, false, "", fmt.Errorf("local_shell: cwd %q cannot be made absolute: %w", d, err)
			}
			cwd = absCwd
		}
		if t, ok := v["timeout"].(string); ok {
			d, err := time.ParseDuration(t)
			if err != nil {
				return "", nil, "", 0, false, "", recoverablef("local_shell: timeout %q is not a duration (write it as 30s, 2m, 1h): %v", t, err)
			}
			if timeout == h.defaultTimeout {
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

type commandResolution struct {
	Command   string
	Path      string
	LookupErr error
}

func resolveCommand(command string) commandResolution {
	res := commandResolution{Command: command}
	if filepath.IsAbs(command) {
		res.Path = filepath.Clean(command)
		return res
	}
	path, err := exec.LookPath(command)
	if err != nil {
		res.LookupErr = err
		res.Path = filepath.Clean(command)
		return res
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		res.LookupErr = err
		res.Path = filepath.Clean(path)
		return res
	}
	res.Path = abs
	return res
}

func commandEntryMatches(entry, command, resolved string) bool {
	clean := filepath.Clean(entry)
	if clean == resolved || clean == command {
		return true
	}
	found := resolveCommand(entry)
	return found.LookupErr == nil && found.Path == resolved
}

func (h *LocalExecTools) checkAllowlist(command string, argsSlice []string, useShell bool, allowedCommands []string, allowedDir string, deniedCommands []string, toolDirs []string) (commandResolution, error) {
	if h.gate == GatePolicy {
		allowedCommands = nil
	}
	if h.gate == GateRequireAllowlist {
		if len(allowedCommands) == 0 {
			return resolveCommand(command), &PolicyRefusal{
				Code:           RefusalUngated,
				Command:        command,
				Project:        allowedDir,
				DeniedCommands: deniedCommands,
				ToolDirs:       toolDirs,
				Hint:           "enable HITL for this session, or configure _allowed_commands for the local_shell tools policy",
			}
		}
	}
	if useShell && (len(allowedCommands) > 0 || len(deniedCommands) > 0 || allowedDir != "") {
		// Check the line the shell will actually run — command plus argv — not
		// just the program name, or an argument could carry a second command.
		script := strings.TrimSpace(command + " " + strings.Join(argsSlice, " "))
		resolution := resolveCommand(command)
		if len(allowedCommands) == 0 {
			if h.gate != GatePolicy {
				return resolution, &PolicyRefusal{
					Code:     RefusalShellNeedsAllowlist,
					Command:  command,
					Project:  allowedDir,
					Hint:     "set shell:false and pass the program in \"command\" with its arguments in \"args\", or name the programs this line may start with in _allowed_commands",
					ToolDirs: toolDirs,
				}
			}
		} else if !hitlservice.CommandLineAllowedByPrefix(script, strings.Join(allowedCommands, ",")) {
			return resolution, fmt.Errorf("local_shell: %q is not one of the allowed commands, or it carries a character the shell would evaluate (a pipe, redirect, substitution or separator); run it without shell:true or name it in _allowed_commands. This is a call-shape problem, not a permission one: a line with shell syntax is never allowlisted", script)
		}
		if fields := strings.Fields(script); len(fields) > 0 {
			command = fields[0]
		}
	}

	resolution := resolveCommand(command)
	resolved := resolution.Path

	if len(deniedCommands) > 0 {
		base := filepath.Base(resolved)
		for _, d := range deniedCommands {
			dClean := filepath.Clean(d)
			if dClean == resolved || dClean == command || filepath.Base(dClean) == base || dClean == base {
				return resolution, &PolicyRefusal{
					Code:            RefusalDenied,
					Command:         command,
					Resolved:        resolved,
					DeniedCommands:  deniedCommands,
					AllowedCommands: allowedCommands,
					Project:         allowedDir,
					Hint:            "this program is on _denied_commands; ask the user to remove it from the local_shell policy if the work needs it",
					ToolDirs:        toolDirs,
				}
			}
		}
	}
	if allowedDir != "" && resolution.LookupErr == nil {
		absDir, err := filepath.Abs(allowedDir)
		if err != nil {
			return resolution, fmt.Errorf("local_shell: allowed dir invalid: %w", err)
		}
		if !CommandLocationAllowed(resolved, absDir, toolDirs) {
			return resolution, &PolicyRefusal{
				Code:            RefusalOutsideProject,
				Command:         command,
				Resolved:        resolved,
				Project:         absDir,
				AllowedCommands: allowedCommands,
				DeniedCommands:  deniedCommands,
				ToolDirs:        toolDirs,
				Hint: fmt.Sprintf("add %q to _allowed_tool_dirs under [tools_policies.local_shell], install the tool inside the project, or set _allowed_dir to the session workspace rather than a fixed path",
					filepath.Dir(resolved)),
			}
		}
	}
	if len(allowedCommands) > 0 {
		allowed := false
		for _, c := range allowedCommands {
			if commandEntryMatches(c, command, resolved) {
				allowed = true
				break
			}
		}
		if !allowed {
			if strings.ContainsAny(command, " \t") {
				return resolution, fmt.Errorf("local_shell: command %q is not in allowlist — put the program alone in \"command\" (for example \"git\") and its arguments in \"args\"", command)
			}
			return resolution, &PolicyRefusal{
				Code:            RefusalNotAllowlisted,
				Command:         command,
				Resolved:        resolved,
				Project:         allowedDir,
				AllowedCommands: allowedCommands,
				DeniedCommands:  deniedCommands,
				LookupErr:       errorText(resolution.LookupErr),
				ToolDirs:        toolDirs,
				Hint:            "the policy enumerates the programs it permits; name this one in _allowed_commands, or use a program it already allows",
			}
		}
	}
	return resolution, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func asPolicyRefusal(err error) *PolicyRefusal {
	var refusal *PolicyRefusal
	if errors.As(err, &refusal) {
		return refusal
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
		return result, fmt.Errorf("local_shell: run %q: %w", command, runErr)
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
	allowedCommands, allowedDir, deniedCommands, _ := h.resolvePolicy(ctx)
	shellDesc := h.shell.ShellModeDescription()
	desc := "Run a terminal command on the local host. Returns {stdout, stderr, exitCode, success, durationSeconds}. Output is capped at the remaining context budget; when it exceeds the cap the FULL output is spooled to a file and stdout/stderr instead carry a 20%-head/80%-tail slice (errors cluster at the tail) with an inline pointer, while the error field names the spool concretely ('full output: <path> (N KiB)') so nothing is lost silently. Errors carry a severity marker you can key on: '(recoverable: adjust parameters and retry)' for anything a corrected call fixes (output too large, bad command, denied path), and '(fatal: <reason>)' only when the environment is broken (disk full, spool unwritable) and retrying will not help. For file operations prefer local_fs.* tools: read_file, write_file, sed, find_files. They enforce sandbox boundaries, size limits, and a read-before-write contract that local_shell does not. Use local_shell for operations with no dedicated tool: running tests, builds, git commands, environment inspection. " + shellDesc
	if len(allowedCommands) > 0 {
		desc += " Allowed commands: " + strings.Join(allowedCommands, ", ") + "."
	}
	if allowedDir != "" {
		desc += " Commands must resolve to a binary inside " + allowedDir + ", or inside a standard toolchain location (node, go, python, rust and similar installs, and anything the runtime's PATH resolves)."
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
