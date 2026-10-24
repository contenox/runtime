package localfileapi

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentview"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/vfs"
)

const (
	defaultFindMaxResults = 2000
	defaultFindGlobCount  = 16
	defaultFindGlobBytes  = 256
)

func defaultFindSkipDirs() map[string]bool {
	return map[string]bool{
		".git": true, "node_modules": true, ".venv": true, "__pycache__": true,
		".next": true, "dist": true, ".cache": true, "vendor": true,
		"target": true, ".idea": true, ".vscode": true,
	}
}

type findDone struct {
	Done      bool `json:"done"`
	Matches   int  `json:"matches"`
	Truncated bool `json:"truncated"`
}

func AddWorkspaceFindRoutes(mux *http.ServeMux, factory *vfs.Factory, hitlFor PolicyEvaluatorFactory) {
	if factory == nil {
		return
	}
	h := &findHandler{
		factory:    factory,
		hitlFor:    hitlFor,
		filters:    defaultFilters(),
		maxResults: defaultFindMaxResults,
		globCount:  defaultFindGlobCount,
		globBytes:  defaultFindGlobBytes,
		skipDirs:   defaultFindSkipDirs(),
		services:   map[string]localfileservice.Service{},
	}
	mux.HandleFunc("GET /workspace/find", h.find)
}

type findHandler struct {
	factory    *vfs.Factory
	hitlFor    PolicyEvaluatorFactory
	filters    map[string]FileFilter
	maxResults int
	globCount  int
	globBytes  int
	skipDirs   map[string]bool

	mu       sync.Mutex
	services map[string]localfileservice.Service
}

func (h *findHandler) serviceFor(resolvedRoot string) (localfileservice.Service, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if svc, ok := h.services[resolvedRoot]; ok {
		return svc, nil
	}
	svc, err := localfileservice.New(resolvedRoot)
	if err != nil {
		return nil, err
	}
	h.services[resolvedRoot] = svc
	return svc, nil
}

func (h *findHandler) find(w http.ResponseWriter, r *http.Request) {
	rawGlob := strings.TrimSpace(apiframework.GetQueryParam(r, "glob", "", "Filename match pattern(s), comma-separated; filepath.Match semantics against the basename, or the root-relative path when a pattern contains '/'. A file matches if ANY pattern matches (e.g. \"*.md\" or \"*.go,*.md\")."))
	if rawGlob == "" {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: glob is required", apiframework.ErrUnprocessableEntity),
			apiframework.ListOperation)
		return
	}
	if len(rawGlob) > h.globBytes {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: glob exceeds the %d-byte limit", apiframework.ErrUnprocessableEntity, h.globBytes),
			apiframework.ListOperation)
		return
	}
	globs := splitGlobs(rawGlob)
	if len(globs) == 0 {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: glob is required", apiframework.ErrUnprocessableEntity),
			apiframework.ListOperation)
		return
	}
	if len(globs) > h.globCount {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: too many glob patterns (max %d)", apiframework.ErrUnprocessableEntity, h.globCount),
			apiframework.ListOperation)
		return
	}
	for _, g := range globs {
		if _, err := filepath.Match(g, ""); err != nil {
			_ = apiframework.Error(w, r,
				fmt.Errorf("%w: bad glob %q", apiframework.ErrUnprocessableEntity, g),
				apiframework.ListOperation)
			return
		}
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

	pathArg := apiframework.GetQueryParam(r, "path", ".", "Directory to search under, relative to the workspace root; defaults to the whole workspace.")
	startRel, err := localfileservice.NormalizeRelPath(pathArg, true)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	view, err := vfs.OpenView(resolved)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ServerOperation)
		return
	}
	if _, err := view.Resolve(startRel); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	limit := h.maxResults
	if raw := strings.TrimSpace(apiframework.GetQueryParam(r, "limit", "", "Maximum matches to stream before truncating; capped at the server maximum.")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}

	filterName := strings.TrimSpace(apiframework.GetQueryParam(r, "filter", "", "Set to \"agent\" to annotate each match with the agent's per-path access verdict; empty or \"full\" streams raw entries."))
	policyName := strings.TrimSpace(apiframework.GetQueryParam(r, "policy", "", "HITL policy the agent view is evaluated against; empty uses the configured default. Ignored unless filter=agent."))
	var ev *agentview.Evaluator
	if filterName != "" && filterName != "full" {
		if _, known := h.filters[filterName]; !known {
			_ = apiframework.Error(w, r,
				fmt.Errorf("%w: unknown filter %q", apiframework.ErrUnprocessableEntity, filterName),
				apiframework.ListOperation)
			return
		}
		if h.hitlFor == nil {
			_ = apiframework.Error(w, r,
				fmt.Errorf("%w: filter %q is not available on this endpoint", apiframework.ErrUnprocessableEntity, filterName),
				apiframework.ListOperation)
			return
		}
		ev = agentview.NewEvaluator(view, h.hitlFor(policyName), policyName)
	}

	svc, err := h.serviceFor(resolved)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: streaming unsupported", apiframework.ErrInternalServerError),
			apiframework.ServerOperation)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	clientGone := false
	result, _ := svc.Find(r.Context(), localfileservice.FindOptions{
		Path:     pathArg,
		Globs:    globs,
		Limit:    limit,
		SkipDirs: h.skipDirs,
	}, func(e localfileservice.Entry) error {
		entry := Entry{Entry: e}
		if ev != nil {
			v := ev.Verdict(r.Context(), e.Path, e.IsDirectory)
			entry.Access = &v
		}
		if err := writeSSEEvent(w, "match", entry); err != nil {
			clientGone = true
			return err
		}
		flusher.Flush()
		return nil
	})

	if clientGone {
		return
	}
	_ = writeSSEEvent(w, "done", findDone{Done: true, Matches: result.Count, Truncated: result.Truncated})
	flusher.Flush()
}

func splitGlobs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
