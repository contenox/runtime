package localfileapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/workspacereplace"
)

const (
	replacePreviewTTL     = 10 * time.Minute
	replaceMaxStored      = 8
	replaceMaxStoredBytes = 64 << 20
)

type storedReplace struct {
	root    string
	plan    *workspacereplace.Plan
	expires time.Time
	bytes   int
}

type replaceStore struct {
	mu       sync.Mutex
	plans    map[string]*storedReplace
	now      func() time.Time
	maxPlans int
	maxBytes int
	bytes    int
}

func newReplaceStore() *replaceStore {
	return &replaceStore{plans: map[string]*storedReplace{}, now: time.Now, maxPlans: replaceMaxStored, maxBytes: replaceMaxStoredBytes}
}

func planBytes(plan *workspacereplace.Plan) int {
	n := 0
	for _, f := range plan.Files {
		n += len(f.Content)
	}
	return n
}

func (s *replaceStore) drop(id string) {
	if stored, ok := s.plans[id]; ok {
		s.bytes -= stored.bytes
		delete(s.plans, id)
	}
}

func (s *replaceStore) oldest() string {
	oldest := ""
	for k, v := range s.plans {
		if oldest == "" || v.expires.Before(s.plans[oldest].expires) {
			oldest = k
		}
	}
	return oldest
}

func (s *replaceStore) put(root string, plan *workspacereplace.Plan) (string, time.Time) {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := "rp_" + hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.plans {
		if now.After(v.expires) {
			s.drop(k)
		}
	}
	size := planBytes(plan)
	for len(s.plans) > 0 && (len(s.plans) >= s.maxPlans || s.bytes+size > s.maxBytes) {
		s.drop(s.oldest())
	}
	expires := now.Add(replacePreviewTTL)
	s.plans[id] = &storedReplace{root: root, plan: plan, expires: expires, bytes: size}
	s.bytes += size
	return id, expires
}

func (s *replaceStore) keep(id string, stored *storedReplace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.now().After(stored.expires) {
		return
	}
	s.drop(id)
	stored.bytes = planBytes(stored.plan)
	s.plans[id] = stored
	s.bytes += stored.bytes
}

func (s *replaceStore) take(id string) *storedReplace {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.plans[id]
	if !ok {
		return nil
	}
	s.drop(id)
	if s.now().After(stored.expires) {
		return nil
	}
	return stored
}

type replacePreviewResponse struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
	*workspacereplace.Plan
}

type replaceApplyRequest struct {
	Paths []string `json:"paths,omitempty"`
}

type replaceApplied struct {
	Path    string `json:"path"`
	ETag    string `json:"etag"`
	Matches int    `json:"matches"`
}

type replaceConflict struct {
	Path        string `json:"path"`
	CurrentETag string `json:"currentEtag"`
}

type replaceFailed struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type replaceApplyResponse struct {
	Applied   []replaceApplied  `json:"applied"`
	Conflicts []replaceConflict `json:"conflicts"`
	Failed    []replaceFailed   `json:"failed"`
	Retry     bool              `json:"retry"`
}

func (wh *workspaceHandler) replacePreview(w http.ResponseWriter, r *http.Request) {
	root := apiframework.GetQueryParam(r, "root", "", "Workspace root to replace in; empty or \"/\" uses the default root.")
	resolved, ok := wh.factory.Allows(root)
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: workspace root %q is not under any configured workspace root; roots: %s",
				apiframework.ErrUnprocessableEntity, root, wh.factory.DescribeRoots()),
			apiframework.CreateOperation)
		return
	}
	q, err := apiframework.Decode[workspacereplace.Query](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	plan, err := workspacereplace.Build(r.Context(), resolved, q, localtools.WorkspaceSkip(), workspacereplace.DefaultLimits())
	if errors.Is(err, workspacereplace.ErrInvalidQuery) {
		_ = apiframework.Error(w, r, fmt.Errorf("%w: %v", apiframework.ErrUnprocessableEntity, err), apiframework.CreateOperation)
		return
	}
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	id, expires := wh.replaces.put(resolved, plan)
	_ = apiframework.Encode(w, r, http.StatusOK, replacePreviewResponse{ID: id, ExpiresAt: expires, Plan: plan})
}

func (wh *workspaceHandler) replaceApply(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[replaceApplyRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	id := r.PathValue("id")
	stored := wh.replaces.take(id)
	if stored == nil {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: replace preview %q is unknown, expired or already applied", apiframework.ErrNotFound, id),
			apiframework.UpdateOperation)
		return
	}
	if _, ok := wh.factory.Allows(stored.root); !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: the workspace root of this preview is no longer granted", apiframework.ErrUnprocessableEntity),
			apiframework.UpdateOperation)
		return
	}
	files := stored.plan.Files
	if len(req.Paths) > 0 {
		byPath := map[string]workspacereplace.FilePlan{}
		for _, f := range files {
			byPath[f.Path] = f
		}
		chosen := make([]workspacereplace.FilePlan, 0, len(req.Paths))
		for _, p := range req.Paths {
			f, ok := byPath[p]
			if !ok {
				wh.replaces.keep(id, stored)
				_ = apiframework.Error(w, r,
					fmt.Errorf("%w: %s is not part of this preview", apiframework.ErrUnprocessableEntity, p),
					apiframework.UpdateOperation)
				return
			}
			chosen = append(chosen, f)
		}
		files = chosen
	}
	svc, err := wh.serviceFor(stored.root)
	if err != nil {
		wh.replaces.keep(id, stored)
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	h := &handler{service: svc, events: wh.events}
	resp := replaceApplyResponse{Applied: []replaceApplied{}, Conflicts: []replaceConflict{}, Failed: []replaceFailed{}}
	var retry []workspacereplace.FilePlan
	for _, f := range files {
		entry, err := svc.WriteIfMatch(r.Context(), f.Path, f.Content, f.ETag)
		var conflict *localfileservice.ConflictError
		if errors.As(err, &conflict) {
			resp.Conflicts = append(resp.Conflicts, replaceConflict{Path: f.Path, CurrentETag: conflict.Current})
			continue
		}
		if err != nil {
			resp.Failed = append(resp.Failed, replaceFailed{Path: f.Path, Message: failureMessage(svc.Root(), err)})
			retry = append(retry, f)
			continue
		}
		h.saved(r, entry, map[string]any{"replace": id, "matches": f.Matches})
		resp.Applied = append(resp.Applied, replaceApplied{Path: f.Path, ETag: entry.ETag, Matches: f.Matches})
	}
	sort.Slice(resp.Conflicts, func(i, j int) bool { return resp.Conflicts[i].Path < resp.Conflicts[j].Path })
	sort.Slice(resp.Failed, func(i, j int) bool { return resp.Failed[i].Path < resp.Failed[j].Path })
	if len(retry) > 0 {
		kept := *stored.plan
		kept.Files = retry
		kept.TotalMatches = 0
		for _, f := range retry {
			kept.TotalMatches += f.Matches
		}
		wh.replaces.keep(id, &storedReplace{root: stored.root, plan: &kept, expires: stored.expires})
		resp.Retry = true
	}
	_ = apiframework.Encode(w, r, http.StatusOK, resp)
}

func failureMessage(root string, err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	msg := err.Error()
	if root != "" {
		msg = strings.ReplaceAll(msg, filepath.Clean(root)+string(filepath.Separator), "")
	}
	return msg
}
