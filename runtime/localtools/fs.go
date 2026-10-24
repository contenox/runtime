package localtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
)

const LocalFSToolsName = "local_fs"

const readBeforeWriteDenial = "local_fs: cannot modify existing file %s without reading it first. Call local_fs.read_file(%q) to confirm the current contents, then retry. " + severityRecoverable

const fileUnchangedStub = "File unchanged since last read — the content from your earlier read_file call in this conversation is still current. Pass force=true if you need the content re-sent."

const readBeforeWriteFullReadDenial = "local_fs: cannot overwrite existing file %s after only reading a line range. Call local_fs.read_file(%q) to read the full current contents, then retry. " + severityRecoverable

const readBeforeWriteStaleReadDenial = "local_fs: cannot modify existing file %s because it changed since you read it. Call local_fs.read_file(%q) to refresh the current contents, then retry. " + severityRecoverable

type readRequirement int

const (
	requireAnyFileRead readRequirement = iota
	requireFullFileRead
)

type StatFileIO interface {
	Stat(ctx context.Context, path string) (os.FileInfo, error)
}

type LocalFSTools struct {
	allowedDir  string
	db          libdb.DBManager
	fileIO      FileIO
	name        string
	cwdResolver func(context.Context) string
	dialect     SQLDialect
	fileIOIsOS  bool
}

type FSOption func(*LocalFSTools)

func WithSQLDialect(d SQLDialect) FSOption {
	return func(h *LocalFSTools) { h.dialect = d }
}

func NewLocalFSTools(allowedDir string, db libdb.DBManager) taskengine.ToolsRepo {
	return NewLocalFSToolsWith(allowedDir, db, nil, LocalFSToolsName, nil)
}

func NewLocalFSToolsWith(allowedDir string, db libdb.DBManager, io FileIO, name string, cwdResolver func(context.Context) string, opts ...FSOption) taskengine.ToolsRepo {
	if io == nil {
		io = osFileIO{}
	}
	_, isOS := io.(osFileIO)
	if name == "" {
		name = LocalFSToolsName
	}
	cleaned := allowedDir
	if cleaned != "" {
		cleaned = filepath.Clean(cleaned)
	}
	h := &LocalFSTools{
		allowedDir:  cleaned,
		db:          db,
		fileIO:      io,
		name:        name,
		cwdResolver: cwdResolver,
		dialect:     DialectSQLite,
		fileIOIsOS:  isOS,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *LocalFSTools) Exec(ctx context.Context, startTime time.Time, input any, debug bool, toolsCall *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	res, dt, err := h.execDispatch(ctx, startTime, input, debug, toolsCall)
	return res, dt, markSeverity(err)
}

func (h *LocalFSTools) execDispatch(ctx context.Context, startTime time.Time, input any, debug bool, toolsCall *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if toolsCall == nil {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: tools required")
	}

	args, ok := input.(map[string]any)
	if !ok {
		if len(toolsCall.Args) > 0 {
			args = make(map[string]any, len(toolsCall.Args))
			for k, v := range toolsCall.Args {
				args[k] = v
			}
		} else {
			return nil, taskengine.DataTypeAny, errors.New("local_fs: input must be a map (or provide tools.args)")
		}
	}

	toolName := toolsCall.ToolName
	if toolName == "" {
		toolName = toolsCall.Name
	}

	switch toolName {
	case "read_file":
		if err := rejectUnknownArgs("local_fs.read_file", args, "path", "start_line", "end_line", "force"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.readFile(ctx, args)
	case "write_file":
		if err := rejectUnknownArgs("local_fs.write_file", args, "path", "content"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.writeFile(ctx, args)
	case "list_dir":
		if err := rejectUnknownArgs("local_fs.list_dir", args, "path", "recursive", "max_depth", "offset"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.listDir(ctx, args)
	case "grep":
		if err := rejectUnknownArgs("local_fs.grep", args, "path", "pattern", "regex", "start_line", "end_line"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.grep(ctx, args)
	case "find_files":
		if err := rejectUnknownArgs("local_fs.find_files", args, "pattern", "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.findFiles(ctx, args)
	case "sed":
		if err := rejectUnknownArgs("local_fs.sed", args, "path", "pattern", "replacement", "all", "expect_replacements", "start_line", "end_line"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.sed(ctx, args)
	case "count_stats":
		if err := rejectUnknownArgs("local_fs.count_stats", args, "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.countStats(ctx, args)
	case "read_file_range":
		if err := rejectUnknownArgs("local_fs.read_file_range", args, "path", "start_line", "end_line"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.readFileRange(ctx, args)
	case "stat_file":
		if err := rejectUnknownArgs("local_fs.stat_file", args, "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.statFile(ctx, args)
	default:
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: unknown tool %s", toolName)
	}
}

func (h *LocalFSTools) baseDir(ctx context.Context) (string, error) {
	if args := h.policyArgs(ctx); len(args) > 0 {
		if policyDir := strings.TrimSpace(args["_allowed_dir"]); policyDir != "" {
			cleaned := filepath.Clean(policyDir)
			if filepath.IsAbs(cleaned) {
				return cleaned, nil
			}
			if h.cwdResolver != nil {
				if cwd := h.cwdResolver(ctx); cwd != "" {
					return filepath.Clean(filepath.Join(cwd, cleaned)), nil
				}
			}
			return cleaned, nil
		}
	}
	base := h.allowedDir
	if base == "" && h.cwdResolver != nil {
		if r := h.cwdResolver(ctx); r != "" {
			base = filepath.Clean(r)
		}
	}
	if base == "" {
		return "", errors.New("local_fs: no allowed directory configured")
	}
	return base, nil
}

func (h *LocalFSTools) absAllowedDir(ctx context.Context) (string, error) {
	base, err := h.baseDir(ctx)
	if err != nil {
		return "", err
	}
	resolved, err := vfs.ResolveRoot(base)
	if err != nil {
		return "", fmt.Errorf("local_fs: invalid allowed dir: %w", err)
	}
	return resolved, nil
}

func (h *LocalFSTools) checkPath(ctx context.Context, path string) (string, error) {
	base, err := h.baseDir(ctx)
	if err != nil {
		return "", err
	}
	resolved, err := vfs.Contain(base, path)
	if err != nil {
		if errors.Is(err, vfs.ErrEscape) {
			return "", fmt.Errorf("local_fs: path %s escapes allowed directory %s", path, base)
		}
		return "", fmt.Errorf("local_fs: %w", err)
	}
	return resolved, nil
}

func (h *LocalFSTools) displayPath(ctx context.Context, absPath string) string {
	base, err := h.absAllowedDir(ctx)
	if err != nil {
		return absPath
	}
	rel, err := filepath.Rel(base, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return absPath
	}
	return filepath.ToSlash(rel)
}

func (h *LocalFSTools) stat(ctx context.Context, absPath string) (os.FileInfo, error) {
	if s, ok := h.fileIO.(StatFileIO); ok {
		return s.Stat(ctx, absPath)
	}
	return os.Stat(absPath)
}

func (h *LocalFSTools) checkDeniedSubstrings(ctx context.Context, absPath string) error {
	subs := h.deniedSubstringsFromPolicy(ctx)
	if len(subs) == 0 {
		return nil
	}
	rel := h.displayPath(ctx, absPath)
	for _, p := range subs {
		if strings.Contains(rel, p) {
			return fmt.Errorf("local_fs: path %q matches denied substring %q (tools_policies.local_fs._denied_path_substrings)", rel, p)
		}
	}
	return nil
}

func (h *LocalFSTools) checkFileSizeLimit(ctx context.Context, absPath string) error {
	info, err := h.stat(ctx, absPath)
	if err != nil {
		return fmt.Errorf("local_fs: stat: %w", err)
	}
	if info.IsDir() {
		return nil
	}
	limit, unlimited := h.maxReadBytesFromPolicy(ctx)
	if unlimited {
		return nil
	}
	if info.Size() > limit {
		return recoverablef("local_fs: file is %d bytes (max %d); use read_file_range or set _max_read_bytes in tools_policies.local_fs", info.Size(), limit)
	}
	return nil
}

func (h *LocalFSTools) precheckFullRead(ctx context.Context, absPath string) error {
	if err := h.checkDeniedSubstrings(ctx, absPath); err != nil {
		return err
	}
	return h.checkFileSizeLimit(ctx, absPath)
}

func (h *LocalFSTools) refuseBinary(ctx context.Context, tool, displayPath, absPath string) error {
	binary, err := sniffBinaryFile(absPath)
	if err != nil || !binary {
		return nil
	}
	detail := "binary file"
	if info, statErr := h.stat(ctx, absPath); statErr == nil {
		detail = fmt.Sprintf("binary file (%s)", fileSizeAndExecFlag(info))
	}
	return recoverablef(
		"local_fs: %s: refusing to read %s: %s. Use stat_file or shell tools for binaries.",
		tool, displayPath, detail)
}

func (h *LocalFSTools) checkToolOutputLimit(ctx context.Context, tool string, payload string) error {
	limit, unlimited := h.maxOutputBytesFromPolicy(ctx)
	if unlimited {
		return nil
	}
	if int64(len(payload)) > limit {
		return recoverablef(
			"local_fs: %s output is %d bytes (max %d); narrow the path or pattern, use read_file_range, or set _max_output_bytes in tools_policies.local_fs",
			tool, len(payload), limit,
		)
	}
	return nil
}

func (h *LocalFSTools) notFound(tool, userPath, absPath string) error {
	msg := fmt.Sprintf("local_fs: %s: %s does not exist", tool, userPath)
	if hint := didYouMean(filepath.Dir(absPath), filepath.Base(absPath)); hint != "" {
		msg += "." + hint
	}
	return recoverablef("%s", msg)
}

func (h *LocalFSTools) resolveTarget(ctx context.Context, tool, path string) (absPath, display string, info os.FileInfo, err error) {
	absPath, err = h.checkPath(ctx, path)
	if err != nil {
		return "", "", nil, err
	}
	if err = h.checkDeniedSubstrings(ctx, absPath); err != nil {
		return "", "", nil, err
	}
	display = h.displayPath(ctx, absPath)
	info, statErr := h.stat(ctx, absPath)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return absPath, display, nil, h.notFound(tool, path, absPath)
		}
		return absPath, display, nil, fmt.Errorf("local_fs: %s: %w", tool, statErr)
	}
	return absPath, display, info, nil
}

func (h *LocalFSTools) readFile(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for read_file")
	}

	absPath, display, info, err := h.resolveTarget(ctx, "read_file", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef(
			"local_fs: read_file: %s is a directory (%s); use list_dir", display, describePathForError(absPath, info))
	}

	if _, hasStart := argFloat(args, "start_line"); hasStart {
		return h.readFileRange(ctx, args)
	}
	if _, hasEnd := argFloat(args, "end_line"); hasEnd {
		return h.readFileRange(ctx, args)
	}

	if err := h.refuseBinary(ctx, "read_file", display, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	outLimit, unlimitedOut := h.maxOutputBytesFromPolicy(ctx)
	readLimit, unlimitedRead := h.maxReadBytesFromPolicy(ctx)

	if !unlimitedRead && info.Size() > readLimit {
		return nil, taskengine.DataTypeAny, recoverablef(
			"local_fs: read_file: %s is %s (%d bytes), over the %d-byte read cap; read a portion with read_file start_line/end_line (e.g. start_line: 1), or raise _max_read_bytes in tools_policies.local_fs to load the whole file",
			display, humanSize(info.Size()), info.Size(), readLimit)
	}

	content, err := h.fileIO.ReadFile(ctx, absPath)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to read file: %w", err)
	}
	if isBinarySample(sniffPrefix(content)) {
		return nil, taskengine.DataTypeAny, recoverablef(
			"local_fs: read_file: refusing to read %s: binary file (%s). Use stat_file or shell tools for binaries.",
			display, fileSizeAndExecFlag(info))
	}

	force, _ := argBool(args, "force")
	hash := contentHash(content)
	if !force && !h.readTrackingDisabled(ctx) && h.hasCurrentFullRead(ctx, absPath, hash) {
		return fileUnchangedStub, taskengine.DataTypeString, nil
	}

	out := string(content)
	if !unlimitedOut && int64(len(out)) > outLimit {
		head, lastLine, nextLine, _ := streamRange(bytes.NewReader(content), 1, math.MaxInt, outLimit)
		total := countTextLines(out)
		h.recordRangeRead(ctx, absPath, content)
		notice := fmt.Sprintf(
			"local_fs: read_file truncated — showed lines 1-%d of %d; output capped at %d bytes. To read the next page call read_file with start_line: %d. %s",
			lastLine, total, outLimit, nextLine, severityRecoverable,
		)
		return head + "\n\n" + notice, taskengine.DataTypeString, nil
	}
	h.recordFullRead(ctx, absPath, content)
	return out, taskengine.DataTypeString, nil
}

func (h *LocalFSTools) readFileRange(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for read_file_range")
	}
	start := 1
	if v, ok := argInt(args, "start_line"); ok {
		if start = v; start < 1 {
			start = 1
		}
	}
	end := math.MaxInt
	if v, ok := argInt(args, "end_line"); ok {
		end = v
	}
	if end < start {
		end = start
	}

	absPath, display, info, err := h.resolveTarget(ctx, "read_file_range", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: read_file_range: %s is a directory; use list_dir", display)
	}
	if err := h.refuseBinary(ctx, "read_file_range", display, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	outLimit, unlimitedOut := h.maxOutputBytesFromPolicy(ctx)
	readLimit, unlimitedRead := h.maxReadBytesFromPolicy(ctx)
	budget := int64(0)
	if !unlimitedOut && outLimit > 0 {
		budget = outLimit
	}

	if !unlimitedRead && info.Size() > readLimit {
		f, err := os.Open(absPath)
		if err != nil {
			return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: read_file_range open: %w", err)
		}
		defer f.Close()
		out, lastLine, nextLine, sErr := streamRange(f, start, end, budget)
		if sErr != nil {
			return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: read_file_range stream: %w", sErr)
		}
		if nextLine != 0 && lastLine < end {
			out += fmt.Sprintf(
				"\n\nlocal_fs: read_file_range truncated — showed lines %d-%d; output capped at %d bytes. To read the next page call read_file with start_line: %d. %s",
				start, lastLine, budget, nextLine, severityRecoverable)
		}
		return out, taskengine.DataTypeString, nil
	}

	content, err := h.fileIO.ReadFile(ctx, absPath)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)
	if start > totalLines {
		h.recordRangeRead(ctx, absPath, content)
		return "", taskengine.DataTypeString, nil
	}
	e := end
	if e > totalLines {
		e = totalLines
	}
	out := strings.Join(lines[start-1:e], "\n")
	h.recordRangeRead(ctx, absPath, content)

	if budget > 0 && int64(len(out)) > budget {
		head, lastLine, nextLine, _ := streamRange(bytes.NewReader([]byte(out)), 1, math.MaxInt, budget)
		absNext := start + nextLine - 1
		notice := fmt.Sprintf(
			"local_fs: read_file_range truncated — showed lines %d-%d; output capped at %d bytes. To read the next page call read_file with start_line: %d. %s",
			start, start+lastLine-1, budget, absNext, severityRecoverable)
		return head + "\n\n" + notice, taskengine.DataTypeString, nil
	}
	return out, taskengine.DataTypeString, nil
}

type FsWriteResult struct {
	Path      string `json:"path"`
	Written   bool   `json:"written"`
	OldBytes  int    `json:"old_bytes"`
	NewBytes  int    `json:"new_bytes"`
	OldSHA256 string `json:"old_sha256"`
	NewSHA256 string `json:"new_sha256"`
	OldText   string `json:"-"`
	NewText   string `json:"-"`
}

func (r FsWriteResult) ToolDiff() (string, string, string, bool) {
	if r.Path == "" || !r.Written || r.OldText == r.NewText {
		return "", "", "", false
	}
	return r.Path, r.OldText, r.NewText, true
}

type FsSedResult struct {
	Path         string `json:"path"`
	Written      bool   `json:"written"`
	Changed      bool   `json:"changed"`
	Replacements int    `json:"replacements"`
	OldBytes     int    `json:"old_bytes"`
	NewBytes     int    `json:"new_bytes"`
	OldSHA256    string `json:"old_sha256"`
	NewSHA256    string `json:"new_sha256"`
	OldText      string `json:"-"`
	NewText      string `json:"-"`
}

func (r FsSedResult) ToolDiff() (string, string, string, bool) {
	if r.Path == "" || !r.Written || r.OldText == r.NewText {
		return "", "", "", false
	}
	return r.Path, r.OldText, r.NewText, true
}

func (h *LocalFSTools) writeFile(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for write_file")
	}
	content, ok := argString(args, "content")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: content required for write_file")
	}

	absPath, err := h.checkPath(ctx, path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := h.checkDeniedSubstrings(ctx, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	display := h.displayPath(ctx, absPath)

	gate := h.requireReadBeforeMutation(ctx, absPath, display, requireFullFileRead)
	if gate.denied {
		return gate.denial, taskengine.DataTypeString, nil
	}
	oldBytes := gate.content

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		if isDiskFull(err) {
			return nil, taskengine.DataTypeAny, fatalf("disk full", "local_fs: failed to create directories for %s", display)
		}
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to create directories: %w", err)
	}

	if gate.exists && gate.verified && !h.readTrackingDisabled(ctx) {
		if unchanged, _ := h.confirmUnchanged(ctx, absPath, gate.hash); !unchanged {
			h.invalidateReads(ctx, absPath)
			return fmt.Sprintf(readBeforeWriteStaleReadDenial, display, display), taskengine.DataTypeString, nil
		}
	}

	if err := h.writeFileDurable(ctx, absPath, []byte(content)); err != nil {
		if isDiskFull(err) {
			return nil, taskengine.DataTypeAny, fatalf("disk full", "local_fs: failed to write file %s", display)
		}
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to write file: %w", err)
	}
	h.invalidateReads(ctx, absPath)

	return FsWriteResult{
		Path:      absPath,
		Written:   true,
		OldBytes:  len(oldBytes),
		NewBytes:  len(content),
		OldSHA256: contentHash(oldBytes),
		NewSHA256: contentHash([]byte(content)),
		OldText:   string(oldBytes),
		NewText:   content,
	}, taskengine.DataTypeJSON, nil
}

func occurrenceLines(text, pattern string, lineOffset, max int) []int {
	var out []int
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, pattern) {
			out = append(out, i+1+lineOffset)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

func (h *LocalFSTools) sed(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for sed")
	}
	pattern, ok := argString(args, "pattern")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: pattern required for sed")
	}
	if pattern == "" {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: sed: pattern must not be empty")
	}
	replacement, ok := argString(args, "replacement")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: replacement required for sed")
	}
	replaceAll, _ := argBool(args, "all")
	expected, hasExpected := argInt(args, "expect_replacements")

	absPath, display, info, err := h.resolveTarget(ctx, "sed", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: sed: %s is a directory", display)
	}
	if err := h.checkFileSizeLimit(ctx, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := h.refuseBinary(ctx, "sed", display, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	gate := h.requireReadBeforeMutation(ctx, absPath, display, requireAnyFileRead)
	if gate.denied {
		return gate.denial, taskengine.DataTypeString, nil
	}
	if !gate.verified {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: sed: could not read %s", display)
	}
	content := gate.content
	oldText := string(content)

	lines := strings.Split(oldText, "\n")
	scopeStart, scopeEnd := 1, len(lines)
	if v, ok := argInt(args, "start_line"); ok && v > 1 {
		scopeStart = v
	}
	if v, ok := argInt(args, "end_line"); ok && v >= scopeStart {
		scopeEnd = v
	}
	if scopeEnd > len(lines) {
		scopeEnd = len(lines)
	}
	scoped := scopeStart > 1 || scopeEnd < len(lines)
	if scopeStart > len(lines) {
		return recoverablef("local_fs: sed: start_line %d is past the end of %s (%d lines); file left unchanged",
			scopeStart, display, len(lines)).Error(), taskengine.DataTypeString, nil
	}

	window := strings.Join(lines[scopeStart-1:scopeEnd], "\n")
	count := strings.Count(window, pattern)

	if count == 0 {
		msg := fmt.Sprintf(
			"local_fs: sed: pattern %q not found in %s — file left unchanged; correct the pattern and retry. %s",
			pattern, display, severityRecoverable)
		if near := suggestNearestLines(window, pattern, 2); near != "" {
			msg += "\nClosest lines:\n" + near
		}
		return msg, taskengine.DataTypeString, nil
	}

	switch {
	case hasExpected && count != expected:
		at := occurrenceLines(window, pattern, scopeStart-1, 5)
		return recoverablef(
			"local_fs: sed: expected %d occurrences of %q in %s but found %d (near lines %v); file left unchanged",
			expected, pattern, display, count, at).Error(), taskengine.DataTypeString, nil
	case !hasExpected && !replaceAll && count > 1:
		at := occurrenceLines(window, pattern, scopeStart-1, 5)
		return recoverablef(
			"local_fs: sed: pattern %q occurs %d times in %s (near lines %v); file left unchanged. Extend the pattern to something unique, narrow it with start_line/end_line, or pass all=true to replace every occurrence",
			pattern, count, display, at).Error(), taskengine.DataTypeString, nil
	}

	newWindow := strings.ReplaceAll(window, pattern, replacement)
	var newContent string
	if scoped {
		out := make([]string, 0, len(lines))
		out = append(out, lines[:scopeStart-1]...)
		out = append(out, strings.Split(newWindow, "\n")...)
		out = append(out, lines[scopeEnd:]...)
		newContent = strings.Join(out, "\n")
	} else {
		newContent = newWindow
	}

	if !h.readTrackingDisabled(ctx) {
		if unchanged, _ := h.confirmUnchanged(ctx, absPath, gate.hash); !unchanged {
			h.invalidateReads(ctx, absPath)
			return fmt.Sprintf(readBeforeWriteStaleReadDenial, display, display), taskengine.DataTypeString, nil
		}
	}

	if err := h.writeFileDurable(ctx, absPath, []byte(newContent)); err != nil {
		if isDiskFull(err) {
			return nil, taskengine.DataTypeAny, fatalf("disk full", "local_fs: failed to write file %s", display)
		}
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to write file: %w", err)
	}
	h.invalidateReads(ctx, absPath)

	newBytes := []byte(newContent)
	return FsSedResult{
		Path:         absPath,
		Written:      true,
		Changed:      newContent != oldText,
		Replacements: count,
		OldBytes:     len(content),
		NewBytes:     len(newBytes),
		OldSHA256:    gate.hash,
		NewSHA256:    contentHash(newBytes),
		OldText:      oldText,
		NewText:      newContent,
	}, taskengine.DataTypeJSON, nil
}

func (h *LocalFSTools) entryFilterFor(ctx context.Context, absRoot string) entryFilter {
	f := entryFilter{
		skipDirs:   h.skipDirNamesFromPolicy(ctx),
		allowExts:  h.listExtensionsFromPolicy(ctx),
		deniedSubs: h.deniedSubstringsFromPolicy(ctx),
	}
	if h.useGitignoreFromPolicy(ctx) {
		if base, err := h.absAllowedDir(ctx); err == nil {
			f.ignore = gitignoreFor(base)
		}
	}
	return f
}

func (h *LocalFSTools) listDir(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, _ := argString(args, "path")
	if path == "" {
		path = "."
	}
	listRootArg := filepath.Clean(path)

	absRoot, display, st, err := h.resolveTarget(ctx, "list_dir", listRootArg)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if !st.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef(
			"local_fs: list_dir: %s is not a directory: %s",
			display, describePathForError(absRoot, st),
		)
	}

	recursive, _ := argBool(args, "recursive")
	policyMaxDepth := h.maxListDepthFromPolicy(ctx)
	reqDepth := 1
	if recursive {
		reqDepth = 3
		if v, ok := argInt(args, "max_depth"); ok && v >= 1 {
			reqDepth = v
		}
		if reqDepth > policyMaxDepth {
			reqDepth = policyMaxDepth
		}
	}

	offset := 0
	if v, ok := argInt(args, "offset"); ok && v > 0 {
		offset = v
	}

	budget, unlimited := h.maxOutputBytesFromPolicy(ctx)
	if unlimited {
		budget = 0
	}
	if budget > 1024 {
		budget -= 512
	}

	filter := h.entryFilterFor(ctx, absRoot)
	baseRoot, baseErr := h.absAllowedDir(ctx)
	if baseErr != nil {
		baseRoot = absRoot
	}

	c := &listCollector{
		budget:  budget,
		offset:  offset,
		maxScan: h.maxListEntriesScannedFromPolicy(ctx),
	}

	if recursive {
		if err := h.walkListDir(ctx, listRootArg, absRoot, baseRoot, "", 1, reqDepth, filter, c); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
	} else {
		if err := h.listOneLevel(ctx, absRoot, baseRoot, filter, c); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
	}

	out := strings.Join(c.out, "\n")
	if c.truncated {
		if out != "" {
			out += "\n"
		}
		out += fmt.Sprintf(
			"local_fs: list_dir truncated — showed %d entries starting at offset %d; output capped at %d bytes. To continue call list_dir with offset: %d (same path, recursive, and max_depth). %s",
			len(c.out), offset, budget, c.nextOffset(), severityRecoverable)
	}
	if len(c.out) == 0 && !c.truncated {
		return "", taskengine.DataTypeString, nil
	}
	return out, taskengine.DataTypeString, nil
}

func (h *LocalFSTools) listOneLevel(ctx context.Context, absRoot, baseRoot string, filter entryFilter, c *listCollector) error {
	entries, err := os.ReadDir(absRoot)
	if err != nil {
		return fmt.Errorf("local_fs: failed to read directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if !c.visit() {
			return nil
		}
		abs := filepath.Join(absRoot, e.Name())
		relFromBase := e.Name()
		if r, relErr := filepath.Rel(baseRoot, abs); relErr == nil {
			relFromBase = filepath.ToSlash(r)
		}
		if filter.skip(relFromBase, e.Name(), e.IsDir()) {
			continue
		}
		if e.IsDir() {
			if !c.add(e.Name() + "/") {
				return nil
			}
			continue
		}
		name := e.Name()
		if info, infoErr := e.Info(); infoErr == nil {
			name += fileEntrySuffix(info)
		}
		if !c.add(name) {
			return nil
		}
	}
	return nil
}

func (h *LocalFSTools) walkListDir(
	ctx context.Context,
	listRootArg, curAbs, baseRoot, relFromListRoot string,
	depth, maxDepth int,
	filter entryFilter,
	c *listCollector,
) error {
	entries, err := os.ReadDir(curAbs)
	if err != nil {
		if depth > 1 {
			return nil
		}
		return fmt.Errorf("local_fs: failed to read directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if !c.visit() {
			return nil
		}

		rel := e.Name()
		if relFromListRoot != "" {
			rel = filepath.ToSlash(filepath.Join(relFromListRoot, e.Name()))
		}

		userPath := rel
		if listRootArg != "" && listRootArg != "." {
			userPath = filepath.ToSlash(filepath.Join(listRootArg, rel))
		}

		childAbs := filepath.Join(curAbs, e.Name())
		relFromBase := userPath
		if r, relErr := filepath.Rel(baseRoot, childAbs); relErr == nil {
			relFromBase = filepath.ToSlash(r)
		}

		if filter.skip(relFromBase, e.Name(), e.IsDir()) {
			continue
		}

		if e.IsDir() {
			if !c.add(userPath + "/") {
				return nil
			}
			if depth >= maxDepth {
				continue
			}
			if err := h.walkListDir(ctx, listRootArg, childAbs, baseRoot, rel, depth+1, maxDepth, filter, c); err != nil {
				return err
			}
			if c.truncated {
				return nil
			}
			continue
		}

		name := userPath
		if info, infoErr := e.Info(); infoErr == nil {
			name += fileEntrySuffix(info)
		}
		if !c.add(name) {
			return nil
		}
	}
	return nil
}

func grepLineRange(args map[string]any, numLines int) (start, end int) {
	start = 1
	end = numLines
	if v, ok := argInt(args, "start_line"); ok {
		if v < 1 {
			v = 1
		}
		start = v
	}
	if v, ok := argInt(args, "end_line"); ok {
		if v < start {
			v = start
		}
		end = v
	}
	if end > numLines {
		end = numLines
	}
	if start > numLines {
		start = numLines + 1
	}
	if end < start {
		end = start - 1
	}
	return start, end
}

func (h *LocalFSTools) grep(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for grep")
	}
	pattern, ok := argString(args, "pattern")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: pattern required for grep")
	}
	if len(pattern) > 8192 {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: grep: pattern exceeds 8192 characters")
	}

	useRegex, _ := argBool(args, "regex")
	var re *regexp.Regexp
	if useRegex {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, taskengine.DataTypeAny, recoverablef("local_fs: grep: invalid regex: %v", err)
		}
	}

	absPath, display, info, err := h.resolveTarget(ctx, "grep", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef(
			"local_fs: grep: %s is a directory; grep searches a single file — use find_files to locate candidates first", display)
	}
	if err := h.checkFileSizeLimit(ctx, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := h.refuseBinary(ctx, "grep", display, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	content, err := h.fileIO.ReadFile(ctx, absPath)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	start, end := grepLineRange(args, len(lines))
	maxMatches := h.maxGrepMatchesFromPolicy(ctx)
	budget, unlimited := h.maxOutputBytesFromPolicy(ctx)
	if unlimited {
		budget = 0
	}
	if budget > 1024 {
		budget -= 512
	}

	var (
		matches   []string
		size      int64
		truncated bool
		hitCap    bool
		lastLine  = end
	)
	for lineNo := start; lineNo <= end; lineNo++ {
		if lineNo < 1 || lineNo > len(lines) {
			continue
		}
		line := lines[lineNo-1]
		var matched bool
		if useRegex {
			matched = re.MatchString(line)
		} else {
			matched = strings.Contains(line, pattern)
		}
		if !matched {
			continue
		}
		entry := fmt.Sprintf("%d: %s", lineNo, line)
		if budget > 0 && size+int64(len(entry)+1) > budget {
			truncated = true
			lastLine = lineNo - 1
			break
		}
		matches = append(matches, entry)
		size += int64(len(entry) + 1)
		if len(matches) >= maxMatches {
			truncated = true
			hitCap = true
			lastLine = lineNo
			break
		}
	}

	out := strings.Join(matches, "\n")
	if truncated {
		if out != "" {
			out += "\n"
		}
		noun := "matches"
		if len(matches) == 1 {
			noun = "match"
		}
		reason := fmt.Sprintf("output capped at %d bytes (raise _max_output_bytes or _model_context_tokens in tools_policies.local_fs)", budget)
		if hitCap {
			reason = fmt.Sprintf("hit the %d-match cap (raise _max_grep_matches in tools_policies.local_fs)", maxMatches)
		}
		out += fmt.Sprintf(
			"local_fs: grep truncated — %d %s shown, searched lines %d-%d of %d; %s. Narrow the pattern or continue with start_line: %d. %s",
			len(matches), noun, start, lastLine, len(lines), reason, lastLine+1, severityRecoverable)
	}
	return out, taskengine.DataTypeString, nil
}

func (h *LocalFSTools) findFiles(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	pattern, ok := argString(args, "pattern")
	if !ok || pattern == "" {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: pattern required for find_files")
	}
	rootArg, _ := argString(args, "path")
	if rootArg == "" {
		rootArg = "."
	}

	absRoot, display, info, err := h.resolveTarget(ctx, "find_files", filepath.Clean(rootArg))
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if !info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: find_files: %s is not a directory", display)
	}

	patternHasSlash := strings.ContainsRune(pattern, '/')
	filter := h.entryFilterFor(ctx, absRoot)
	maxResults := h.maxFindResultsFromPolicy(ctx)
	maxDepth := h.maxFindDepthFromPolicy(ctx)
	baseRoot, baseErr := h.absAllowedDir(ctx)
	if baseErr != nil {
		baseRoot = absRoot
	}

	var matches []string
	truncated := false

	walkErr := filepath.WalkDir(absRoot, func(walkPath string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if truncated {
			return filepath.SkipAll
		}

		rel, relErr := filepath.Rel(absRoot, walkPath)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		relFromBase := rel
		if r, e := filepath.Rel(baseRoot, walkPath); e == nil {
			relFromBase = filepath.ToSlash(r)
		}

		if d.IsDir() {
			if filter.skip(relFromBase, d.Name(), true) {
				return filepath.SkipDir
			}
			if strings.Count(rel, "/")+1 > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if filter.skip(relFromBase, d.Name(), false) {
			return nil
		}

		var matched bool
		if patternHasSlash {
			matched, _ = filepath.Match(pattern, rel)
		} else {
			matched, _ = filepath.Match(pattern, d.Name())
		}
		if matched {
			matches = append(matches, rel)
			if len(matches) >= maxResults {
				truncated = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: find_files: %w", walkErr)
	}
	if matches == nil {
		matches = []string{}
	}

	type findResult struct {
		Matches   []string `json:"matches"`
		Count     int      `json:"count"`
		Truncated bool     `json:"truncated,omitempty"`
		Note      string   `json:"note,omitempty"`
	}
	res := findResult{Matches: matches, Count: len(matches), Truncated: truncated}
	if truncated {
		res.Note = fmt.Sprintf("capped at %d results; narrow the pattern or search a subdirectory", maxResults)
	}
	out, err := json.Marshal(res)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: find_files marshal: %w", err)
	}
	s := string(out)
	if err := h.checkToolOutputLimit(ctx, "find_files", s); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	return s, taskengine.DataTypeJSON, nil
}

func (h *LocalFSTools) countStats(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for count_stats")
	}

	absPath, display, info, err := h.resolveTarget(ctx, "count_stats", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if info.IsDir() {
		return nil, taskengine.DataTypeAny, recoverablef("local_fs: count_stats: %s is a directory", display)
	}
	if err := h.checkFileSizeLimit(ctx, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := h.refuseBinary(ctx, "count_stats", display, absPath); err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	content, err := h.fileIO.ReadFile(ctx, absPath)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: failed to read file: %w", err)
	}

	text := string(content)
	lineCount := countTextLines(text)
	if len(content) > 0 && content[len(content)-1] == '\n' {
		lineCount--
	}
	result := fmt.Sprintf("Lines: %d, Words: %d, Bytes: %d", lineCount, len(strings.Fields(text)), len(content))
	if err := h.checkToolOutputLimit(ctx, "count_stats", result); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	return result, taskengine.DataTypeString, nil
}

func (h *LocalFSTools) statFile(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	path, ok := argString(args, "path")
	if !ok {
		return nil, taskengine.DataTypeAny, errors.New("local_fs: path required for stat_file")
	}

	absPath, _, info, err := h.resolveTarget(ctx, "stat_file", path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}

	binary := false
	if info.Mode().IsRegular() {
		if b, sniffErr := sniffBinaryFile(absPath); sniffErr == nil {
			binary = b
		}
	}

	result := map[string]any{
		"name":       info.Name(),
		"size":       info.Size(),
		"sizeHuman":  humanSize(info.Size()),
		"modTime":    info.ModTime().Format(time.RFC3339),
		"isDir":      info.IsDir(),
		"mode":       info.Mode().String(),
		"executable": isExecutable(info),
		"binary":     binary,
	}

	b, err := json.Marshal(result)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("local_fs: marshal stat: %w", err)
	}
	out := string(b)
	if err := h.checkToolOutputLimit(ctx, "stat_file", out); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	return out, taskengine.DataTypeJSON, nil
}

var _ taskengine.ToolsRepo = (*LocalFSTools)(nil)
