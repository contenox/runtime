package workspacereplace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/contenox/runtime/runtime/localfileservice"
)

var ErrInvalidQuery = errors.New("invalid replace query")

type Query struct {
	Pattern       string   `json:"pattern"`
	Replacement   string   `json:"replacement"`
	Regex         bool     `json:"regex"`
	CaseSensitive bool     `json:"caseSensitive"`
	WholeWord     bool     `json:"wholeWord"`
	Include       []string `json:"include,omitempty"`
	Exclude       []string `json:"exclude,omitempty"`
	Path          string   `json:"path,omitempty"`
}

type Limits struct {
	MaxPatternBytes int
	MaxFilesScanned int
	MaxFileBytes    int64
	MaxMatches      int
	MaxPlanBytes    int
	MaxHunksPerFile int
}

func DefaultLimits() Limits {
	return Limits{
		MaxPatternBytes: 512,
		MaxFilesScanned: 20000,
		MaxFileBytes:    2 << 20,
		MaxMatches:      5000,
		MaxPlanBytes:    32 << 20,
		MaxHunksPerFile: 200,
	}
}

type Hunk struct {
	Line   int      `json:"line"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}

type FilePlan struct {
	Path           string `json:"path"`
	ETag           string `json:"etag"`
	Matches        int    `json:"matches"`
	Hunks          []Hunk `json:"hunks"`
	HunksTruncated bool   `json:"hunksTruncated,omitempty"`
	Content        []byte `json:"-"`
}

type Plan struct {
	Files        []FilePlan `json:"files"`
	TotalMatches int        `json:"totalMatches"`
	FilesScanned int        `json:"filesScanned"`
	Truncated    bool       `json:"truncated"`
}

type SkipFunc func(root, rel string, isDir bool) bool

type matcher struct {
	re          *regexp.Regexp
	literal     bool
	wholeWord   bool
	replacement []byte
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

func (m *matcher) wholeWordAt(content []byte, start, end int) bool {
	if !m.wholeWord {
		return true
	}
	if isWordByte(content[start]) && start > 0 && isWordByte(content[start-1]) {
		return false
	}
	if isWordByte(content[end-1]) && end < len(content) && isWordByte(content[end]) {
		return false
	}
	return true
}

func compile(q Query, lim Limits) (*matcher, error) {
	if q.Pattern == "" {
		return nil, fmt.Errorf("%w: pattern is required", ErrInvalidQuery)
	}
	if len(q.Pattern) > lim.MaxPatternBytes {
		return nil, fmt.Errorf("%w: pattern exceeds the %d-byte limit", ErrInvalidQuery, lim.MaxPatternBytes)
	}
	expr := q.Pattern
	if !q.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	flags := "(?m)"
	if !q.CaseSensitive {
		flags = "(?im)"
	}
	re, err := regexp.Compile(flags + expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	for _, g := range append(append([]string{}, q.Include...), q.Exclude...) {
		if _, err := path.Match(g, ""); err != nil {
			return nil, fmt.Errorf("%w: bad glob %q", ErrInvalidQuery, g)
		}
	}
	return &matcher{re: re, literal: !q.Regex, wholeWord: q.WholeWord, replacement: []byte(q.Replacement)}, nil
}

func (m *matcher) expand(content []byte, idx []int) []byte {
	if m.literal {
		return m.replacement
	}
	return m.re.Expand(nil, m.replacement, content, idx)
}

func Build(ctx context.Context, root string, q Query, skip SkipFunc, lim Limits) (*Plan, error) {
	m, err := compile(q, lim)
	if err != nil {
		return nil, err
	}
	start := root
	if strings.TrimSpace(q.Path) != "" {
		rel, err := localfileservice.NormalizeRelPath(q.Path, true)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
		}
		if rel != "." {
			if skip != nil && skip(root, rel, true) {
				return nil, fmt.Errorf("%w: %s is not searchable", ErrInvalidQuery, rel)
			}
			start = filepath.Join(root, filepath.FromSlash(rel))
		}
	}
	info, err := os.Lstat(start)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a folder in the workspace", ErrInvalidQuery, q.Path)
	}

	plan := &Plan{Files: []FilePlan{}}
	planBytes := 0
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p != start && skip != nil && skip(root, rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if skip != nil && skip(root, rel, false) {
			return nil
		}
		if len(q.Include) > 0 && !localfileservice.MatchesAnyGlob(q.Include, rel, d.Name()) {
			return nil
		}
		if len(q.Exclude) > 0 && localfileservice.MatchesAnyGlob(q.Exclude, rel, d.Name()) {
			return nil
		}
		if plan.FilesScanned >= lim.MaxFilesScanned {
			plan.Truncated = true
			return filepath.SkipAll
		}
		plan.FilesScanned++
		fi, ierr := d.Info()
		if ierr != nil || fi.Size() > lim.MaxFileBytes {
			return nil
		}
		content, rderr := os.ReadFile(p)
		if rderr != nil || isBinary(content) {
			return nil
		}
		fp, ok := planFile(m, rel, content, lim)
		if !ok {
			return nil
		}
		if planBytes+len(fp.Content) > lim.MaxPlanBytes {
			plan.Truncated = true
			return filepath.SkipAll
		}
		planBytes += len(fp.Content)
		plan.Files = append(plan.Files, fp)
		plan.TotalMatches += fp.Matches
		if plan.TotalMatches >= lim.MaxMatches {
			plan.Truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return nil, walkErr
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })
	return plan, nil
}

func isBinary(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

type span struct {
	start, end int
	rep        []byte
}

func planFile(m *matcher, rel string, content []byte, lim Limits) (FilePlan, bool) {
	var spans []span
	for _, idx := range m.re.FindAllSubmatchIndex(content, -1) {
		if idx[1] == idx[0] || !m.wholeWordAt(content, idx[0], idx[1]) {
			continue
		}
		spans = append(spans, span{start: idx[0], end: idx[1], rep: m.expand(content, idx)})
	}
	if len(spans) == 0 {
		return FilePlan{}, false
	}
	var out bytes.Buffer
	out.Grow(len(content))
	last := 0
	for _, s := range spans {
		out.Write(content[last:s.start])
		out.Write(s.rep)
		last = s.end
	}
	out.Write(content[last:])

	breaks := []int{}
	for i, b := range content {
		if b == '\n' {
			breaks = append(breaks, i)
		}
	}
	lineOf := func(off int) int { return sort.SearchInts(breaks, off) }
	lineStart := func(line int) int {
		if line == 0 {
			return 0
		}
		return breaks[line-1] + 1
	}
	lineEnd := func(line int) int {
		if line < len(breaks) {
			return breaks[line]
		}
		return len(content)
	}

	endLine := func(sp span) int {
		if content[sp.end-1] == '\n' {
			return lineOf(sp.end)
		}
		return lineOf(sp.end - 1)
	}

	fp := FilePlan{Path: rel, ETag: localfileservice.ETag(content), Matches: len(spans), Content: out.Bytes()}
	for i := 0; i < len(spans); {
		first := lineOf(spans[i].start)
		lastLine := endLine(spans[i])
		j := i + 1
		for j < len(spans) && lineOf(spans[j].start) <= lastLine {
			if l := endLine(spans[j]); l > lastLine {
				lastLine = l
			}
			j++
		}
		if len(fp.Hunks) >= lim.MaxHunksPerFile {
			fp.HunksTruncated = true
			break
		}
		from, to := lineStart(first), lineEnd(lastLine)
		var after bytes.Buffer
		cursor := from
		for _, s := range spans[i:j] {
			after.Write(content[cursor:s.start])
			after.Write(s.rep)
			cursor = s.end
		}
		after.Write(content[cursor:to])
		fp.Hunks = append(fp.Hunks, Hunk{Line: first + 1, Before: lines(content[from:to]), After: lines(after.Bytes())})
		i = j
	}
	return fp, true
}

func lines(b []byte) []string {
	parts := strings.Split(string(b), "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}
