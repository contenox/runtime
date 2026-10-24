package missionchanges

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/missionservice"
)

const (
	weightEdit    = 4
	weightDelete  = 4
	weightMove    = 3
	weightRead    = 2
	weightFetch   = 2
	weightExecute = 2
	weightOther   = 1
)

const maxChangedFiles = 100

const diffDisplayCap = 128 * 1024

const maxOutsidePaths = 20

type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Score  int    `json:"score"`
}

const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

type ScopeStats struct {
	Files        int      `json:"files"`
	Dirs         int      `json:"dirs"`
	Anomaly      bool     `json:"anomaly"`
	OutsidePaths []string `json:"outsidePaths,omitempty"`
}

type Changes struct {
	Files      []ChangedFile `json:"files"`
	Incomplete bool          `json:"incomplete"`
	Scope      ScopeStats    `json:"scope"`
}

type Diff struct {
	Original  string `json:"original"`
	Modified  string `json:"modified"`
	Truncated bool   `json:"truncated,omitempty"`
}

type missionGetter interface {
	Get(ctx context.Context, id string) (*missionservice.Mission, error)
}

type SessionJournalReader interface {
	SessionJournal(instanceID string, sessionID libacp.SessionID) ([]libacp.SessionNotification, string, bool)
}

type Service interface {
	Changes(ctx context.Context, missionID string) (*Changes, error)

	Diff(ctx context.Context, missionID, filePath string) (*Diff, error)
}

type service struct {
	missions missionGetter
	journal  SessionJournalReader
}

func New(missions missionGetter, journal SessionJournalReader) Service {
	return &service{missions: missions, journal: journal}
}

func (s *service) Changes(ctx context.Context, missionID string) (*Changes, error) {
	updates, cwd, err := s.load(ctx, missionID)
	if err != nil {
		return nil, err
	}
	folded := fold(updates)
	return folded.changes(cwd), nil
}

func (s *service) Diff(ctx context.Context, missionID, filePath string) (*Diff, error) {
	updates, _, err := s.load(ctx, missionID)
	if err != nil {
		return nil, err
	}
	folded := fold(updates)
	fileDiff, ok := folded.files[filePath]
	if !ok {
		return nil, apiframework.NotFound("no changed file at that path in this mission")
	}
	original, modified, truncated := capDiff(fileDiff.firstOld, fileDiff.lastNew)
	return &Diff{Original: original, Modified: modified, Truncated: truncated}, nil
}

func (s *service) load(ctx context.Context, missionID string) ([]libacp.SessionNotification, string, error) {
	m, err := s.missions.Get(ctx, missionID)
	if err != nil {
		return nil, "", err
	}
	if s.journal == nil || m.SessionID == "" || m.InstanceID == "" {
		return nil, "", nil
	}
	updates, cwd, ok := s.journal.SessionJournal(m.InstanceID, libacp.SessionID(m.SessionID))
	if !ok {
		return nil, "", nil
	}
	return updates, cwd, nil
}

type fileFold struct {
	firstOld    string
	lastNew     string
	haveFirst   bool
	firstSeenAt int
}

type folded struct {
	files      map[string]*fileFold
	scores     map[string]int
	touched    map[string]struct{}
	touchOrder []string
}

func fold(updates []libacp.SessionNotification) *folded {
	f := &folded{
		files:   make(map[string]*fileFold),
		scores:  make(map[string]int),
		touched: make(map[string]struct{}),
	}
	callWeights := make(map[string]map[string]int)
	for i, n := range updates {
		u := n.Update
		if u.SessionUpdate != libacp.SessionUpdateToolCall && u.SessionUpdate != libacp.SessionUpdateToolCallUpdate {
			continue
		}
		callID := u.ToolCallID
		if callID == "" {
			callID = fmt.Sprintf("\x00idx%d", i)
		}
		cw := callWeights[callID]
		if cw == nil {
			cw = make(map[string]int)
			callWeights[callID] = cw
		}

		editPaths := make(map[string]struct{})
		for _, c := range u.ToolContent {
			if c.Type != libacp.ToolCallContentDiff || c.Path == "" {
				continue
			}
			editPaths[c.Path] = struct{}{}
			f.markTouched(c.Path)
			ff := f.files[c.Path]
			if ff == nil {
				ff = &fileFold{firstSeenAt: i}
				f.files[c.Path] = ff
			}
			if !ff.haveFirst {
				ff.firstOld = c.OldText
				ff.haveFirst = true
			}
			ff.lastNew = c.NewText
			cw[c.Path] = weightEdit
		}
		for _, loc := range u.Locations {
			if loc.Path == "" {
				continue
			}
			f.markTouched(loc.Path)
			if _, isEdit := editPaths[loc.Path]; isEdit {
				continue
			}
			if w := weightForKind(u.Kind); w > cw[loc.Path] {
				cw[loc.Path] = w
			}
		}
	}
	for _, cw := range callWeights {
		for p, w := range cw {
			f.scores[p] += w
		}
	}
	return f
}

func (f *folded) markTouched(p string) {
	if _, seen := f.touched[p]; seen {
		return
	}
	f.touched[p] = struct{}{}
	f.touchOrder = append(f.touchOrder, p)
}

func weightForKind(k libacp.ToolKind) int {
	switch k {
	case libacp.ToolKindEdit:
		return weightEdit
	case libacp.ToolKindDelete:
		return weightDelete
	case libacp.ToolKindMove:
		return weightMove
	case libacp.ToolKindRead, libacp.ToolKindSearch:
		return weightRead
	case libacp.ToolKindFetch:
		return weightFetch
	case libacp.ToolKindExecute:
		return weightExecute
	default:
		return weightOther
	}
}

func (f *folded) changes(cwd string) *Changes {
	files := make([]ChangedFile, 0, len(f.files))
	for p, ff := range f.files {
		files = append(files, ChangedFile{
			Path:   p,
			Status: statusFor(ff),
			Score:  f.scores[p],
		})
	}
	sort.Slice(files, func(a, b int) bool {
		if files[a].Score != files[b].Score {
			return files[a].Score > files[b].Score
		}
		fa, fb := f.files[files[a].Path], f.files[files[b].Path]
		if fa.firstSeenAt != fb.firstSeenAt {
			return fa.firstSeenAt < fb.firstSeenAt
		}
		return files[a].Path < files[b].Path
	})

	incomplete := false
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		incomplete = true
	}

	return &Changes{
		Files:      files,
		Incomplete: incomplete,
		Scope:      f.scope(cwd),
	}
}

func statusFor(ff *fileFold) string {
	switch {
	case ff.firstOld == "":
		return StatusAdded
	case ff.lastNew == "":
		return StatusDeleted
	default:
		return StatusModified
	}
}

func (f *folded) scope(root string) ScopeStats {
	root = filepath.Clean(root)
	dirs := make(map[string]struct{})
	var outside []string
	for _, p := range f.touchOrder {
		if root != "" && root != "." && isOutside(root, p) {
			if len(outside) < maxOutsidePaths {
				outside = append(outside, p)
			}
		}
		dirs[topLevelDir(root, p)] = struct{}{}
	}
	sort.Strings(outside)
	return ScopeStats{
		Files:        len(f.touched),
		Dirs:         len(dirs),
		Anomaly:      len(outside) > 0,
		OutsidePaths: outside,
	}
}

func isOutside(root, p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func topLevelDir(root, p string) string {
	cp := filepath.Clean(p)
	if root != "" && root != "." && filepath.IsAbs(cp) {
		if rel, err := filepath.Rel(root, cp); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return firstSegment(filepath.ToSlash(rel))
		}
	}
	if filepath.IsAbs(cp) {
		trimmed := strings.TrimPrefix(filepath.ToSlash(cp), "/")
		return "/" + firstSegment(trimmed)
	}
	return firstSegment(filepath.ToSlash(cp))
}

func firstSegment(rel string) string {
	rel = strings.TrimPrefix(rel, "./")
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return "."
}

func capDiff(original, modified string) (string, string, bool) {
	truncated := false
	if len(original) > diffDisplayCap {
		original = original[:diffDisplayCap]
		truncated = true
	}
	if len(modified) > diffDisplayCap {
		modified = modified[:diffDisplayCap]
		truncated = true
	}
	return original, modified, truncated
}
