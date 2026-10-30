package localfileapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/internal/winproc"
	"github.com/contenox/runtime/runtime/vfs"
)

const (
	defaultSearchMaxResults   = 500
	defaultSearchPerFileCap   = 100
	defaultSearchPreviewBytes = 200
	defaultSearchQueryBytes   = 512
	searchScanTokenBytes      = 1 << 20
)

type searchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Length  int    `json:"length"`
	Preview string `json:"preview"`
}

type searchDone struct {
	Done      bool `json:"done"`
	Matches   int  `json:"matches"`
	Truncated bool `json:"truncated"`
}

func AddWorkspaceSearchRoutes(mux *http.ServeMux, factory *vfs.Factory) {
	if factory == nil {
		return
	}
	h := &searchHandler{
		factory:    factory,
		maxResults: defaultSearchMaxResults,
		perFileCap: defaultSearchPerFileCap,
		previewCap: defaultSearchPreviewBytes,
		queryCap:   defaultSearchQueryBytes,
		lookPath:   exec.LookPath,
	}
	mux.HandleFunc("GET /workspace/search", h.search)
}

type searchHandler struct {
	factory    *vfs.Factory
	maxResults int
	perFileCap int
	previewCap int
	queryCap   int
	lookPath   func(string) (string, error)
}

func (h *searchHandler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(apiframework.GetQueryParam(r, "q", "", "Search query, matched as a literal substring (smart-case)."))
	if q == "" {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: q is required", apiframework.ErrUnprocessableEntity),
			apiframework.ListOperation)
		return
	}
	if len(q) > h.queryCap {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: q exceeds the %d-byte limit", apiframework.ErrUnprocessableEntity, h.queryCap),
			apiframework.ListOperation)
		return
	}

	root := apiframework.GetQueryParam(r, "root", "", "Workspace root to search; empty or \"/\" uses the configured default. Must be an allowlisted root or a directory under one.")
	resolved, ok := h.factory.Allows(root)
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: workspace root %q is not under any configured workspace root; roots: %s",
				apiframework.ErrUnprocessableEntity, root, h.factory.DescribeRoots()),
			apiframework.ListOperation)
		return
	}

	limit := h.maxResults
	if raw := strings.TrimSpace(apiframework.GetQueryParam(r, "limit", "", "Maximum matches to stream before truncating; capped at the server maximum.")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}

	rgPath, err := h.lookPath("rg")
	if err != nil {
		writeSearchDependencyMissing(w)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: streaming unsupported", apiframework.ErrInternalServerError),
			apiframework.ServerOperation)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	args := []string{"--json", "--fixed-strings", "--smart-case",
		"-m", strconv.Itoa(h.perFileCap)}
	for _, denied := range vfs.ControlPlaneDenied() {
		if rel, err := filepath.Rel(resolved, denied); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			args = append(args, "--glob", "!"+filepath.ToSlash(rel)+"/**", "--glob", "!"+filepath.ToSlash(rel))
		}
	}
	args = append(args, "--", q, ".")
	cmd := exec.CommandContext(ctx, rgPath, args...)
	winproc.Hide(cmd)
	cmd.Dir = resolved
	cmd.Stderr = io.Discard

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ServerOperation)
		return
	}
	if err := cmd.Start(); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ServerOperation)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		cancel()
		_ = cmd.Wait()
		return
	}
	flusher.Flush()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), searchScanTokenBytes)

	matches := 0
	truncated := false
	clientGone := false

scan:
	for scanner.Scan() {
		frames, isMatch := parseRipgrepMatch(scanner.Bytes(), h.previewCap)
		if !isMatch {
			continue
		}
		for _, m := range frames {
			if matches >= limit {
				truncated = true
				break scan
			}
			if err := writeSSEEvent(w, "match", m); err != nil {
				clientGone = true
				break scan
			}
			flusher.Flush()
			matches++
		}
	}

	cancel()
	_ = cmd.Wait()

	if clientGone {
		return
	}
	_ = writeSSEEvent(w, "done", searchDone{Done: true, Matches: matches, Truncated: truncated})
	flusher.Flush()
}

type ripgrepEnvelope struct {
	Type string `json:"type"`
	Data struct {
		Path       ripgrepText       `json:"path"`
		Lines      ripgrepText       `json:"lines"`
		LineNumber int               `json:"line_number"`
		Submatches []ripgrepSubmatch `json:"submatches"`
	} `json:"data"`
}

type ripgrepText struct {
	Text string `json:"text"`
}

type ripgrepSubmatch struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func parseRipgrepMatch(line []byte, previewCap int) ([]searchMatch, bool) {
	var env ripgrepEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, false
	}
	if env.Type != "match" {
		return nil, false
	}
	path := strings.TrimPrefix(env.Data.Path.Text, "./")
	if path == "" {
		return nil, false
	}
	preview := trimPreview(env.Data.Lines.Text, previewCap)
	frames := make([]searchMatch, 0, len(env.Data.Submatches))
	for _, sm := range env.Data.Submatches {
		frames = append(frames, searchMatch{
			Path:    path,
			Line:    env.Data.LineNumber,
			Column:  sm.Start,
			Length:  sm.End - sm.Start,
			Preview: preview,
		})
	}
	if len(frames) == 0 {
		return nil, false
	}
	return frames, true
}

func trimPreview(s string, previewCap int) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) <= previewCap {
		return s
	}
	s = s[:previewCap]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func writeSSEEvent(w io.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func writeSearchDependencyMissing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "workspace search requires the ripgrep (rg) binary, which is not installed on this host; install ripgrep to enable GET /workspace/search",
			"type":    "api_error",
			"code":    "dependency_missing",
		},
	})
}
