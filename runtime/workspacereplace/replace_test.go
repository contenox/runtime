package workspacereplace_test

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/workspacereplace"
	"github.com/stretchr/testify/require"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	return root
}

func build(t *testing.T, root string, q workspacereplace.Query) *workspacereplace.Plan {
	t.Helper()
	plan, err := workspacereplace.Build(context.Background(), root, q, skipVendor, workspacereplace.DefaultLimits())
	require.NoError(t, err)
	return plan
}

func skipVendor(_, rel string, isDir bool) bool {
	return isDir && path.Base(rel) == "vendor"
}

func paths(plan *workspacereplace.Plan) []string {
	out := []string{}
	for _, f := range plan.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestUnit_Replace_LiteralAcrossFilesWithGlobs(t *testing.T) {
	root := tree(t, map[string]string{
		"a.go":             "package a\n\nfunc OldName() {}\nvar x = OldName\n",
		"pkg/b.go":         "package pkg\n\n// oldname stays\nfunc call() { OldName() }\n",
		"pkg/b.md":         "OldName in docs\n",
		"vendor/dep.go":    "OldName\n",
		"binary.go":        "OldName\x00\x01",
		"pkg/untouched.go": "package pkg\n",
	})
	plan := build(t, root, workspacereplace.Query{Pattern: "OldName", Replacement: "NewName", CaseSensitive: true, Include: []string{"*.go"}})
	require.Equal(t, []string{"a.go", "pkg/b.go"}, paths(plan))
	require.Equal(t, 3, plan.TotalMatches)
	require.False(t, plan.Truncated)

	a := plan.Files[0]
	require.Equal(t, "package a\n\nfunc NewName() {}\nvar x = NewName\n", string(a.Content))
	require.Equal(t, localfileservice.ETag([]byte("package a\n\nfunc OldName() {}\nvar x = OldName\n")), a.ETag)
	require.Equal(t, []workspacereplace.Hunk{
		{Line: 3, Before: []string{"func OldName() {}"}, After: []string{"func NewName() {}"}},
		{Line: 4, Before: []string{"var x = OldName"}, After: []string{"var x = NewName"}},
	}, a.Hunks)

	insensitive := build(t, root, workspacereplace.Query{Pattern: "oldname", Replacement: "NewName", Include: []string{"pkg/*.go"}})
	require.Equal(t, []string{"pkg/b.go"}, paths(insensitive))
	require.Equal(t, 2, insensitive.TotalMatches)

	excluded := build(t, root, workspacereplace.Query{Pattern: "OldName", Replacement: "x", CaseSensitive: true, Exclude: []string{"*.md", "a.go"}})
	require.Equal(t, []string{"pkg/b.go"}, paths(excluded))

	scoped := build(t, root, workspacereplace.Query{Pattern: "OldName", Replacement: "x", CaseSensitive: true, Path: "pkg"})
	require.Equal(t, []string{"pkg/b.go", "pkg/b.md"}, paths(scoped))
}

func TestUnit_Replace_LiteralReplacementIsNotATemplate(t *testing.T) {
	root := tree(t, map[string]string{"price.txt": "cost: X\n"})
	plan := build(t, root, workspacereplace.Query{Pattern: "X", Replacement: "$1.00", CaseSensitive: true})
	require.Equal(t, "cost: $1.00\n", string(plan.Files[0].Content))
}

func TestUnit_Replace_RegexGroupsWholeWordAndLineBreaks(t *testing.T) {
	root := tree(t, map[string]string{
		"calls.go": "log.Printf(\"a\")\nlog.Printf(\"b\")\nxlog.Printf(\"c\")\n",
		"crlf.txt": "one\r\ntwo\r\n",
		"join.txt": "begin\nmiddle\nend\n",
	})
	plan := build(t, root, workspacereplace.Query{Pattern: `log\.Printf\(("[a-z]")\)`, Replacement: `slog.Info($1)`, Regex: true, WholeWord: true, CaseSensitive: true})
	require.Equal(t, []string{"calls.go"}, paths(plan))
	require.Equal(t, "slog.Info(\"a\")\nslog.Info(\"b\")\nxlog.Printf(\"c\")\n", string(plan.Files[0].Content))

	crlf := build(t, root, workspacereplace.Query{Pattern: "two", Replacement: "2", CaseSensitive: true})
	require.Equal(t, "one\r\n2\r\n", string(crlf.Files[0].Content))
	require.Equal(t, []string{"two"}, crlf.Files[0].Hunks[0].Before)
	require.Equal(t, []string{"2"}, crlf.Files[0].Hunks[0].After)

	joined := build(t, root, workspacereplace.Query{Pattern: "begin\nmiddle\n", Replacement: "start\n", Regex: true, CaseSensitive: true})
	require.Equal(t, "start\nend\n", string(joined.Files[0].Content))
	require.Equal(t, []workspacereplace.Hunk{{Line: 1, Before: []string{"begin", "middle", "end"}, After: []string{"start", "end"}}}, joined.Files[0].Hunks)

	atEOF := build(t, root, workspacereplace.Query{Pattern: "end\n", Replacement: "fin\n", Regex: true, CaseSensitive: true})
	require.Equal(t, "begin\nmiddle\nfin\n", string(atEOF.Files[0].Content))

	empty := build(t, root, workspacereplace.Query{Pattern: "x*", Replacement: "y", Regex: true, Include: []string{"crlf.txt"}})
	require.Empty(t, empty.Files, "empty matches are not replacements")
}

func TestUnit_Replace_StopsAtTheMatchLimitOnAFileBoundary(t *testing.T) {
	root := tree(t, map[string]string{"a.txt": "x x x\n", "b.txt": "x x\n", "c.txt": "x\n"})
	lim := workspacereplace.DefaultLimits()
	lim.MaxMatches = 4
	plan, err := workspacereplace.Build(context.Background(), root, workspacereplace.Query{Pattern: "x", Replacement: "y"}, nil, lim)
	require.NoError(t, err)
	require.True(t, plan.Truncated)
	require.Equal(t, []string{"a.txt", "b.txt"}, paths(plan))
	require.Equal(t, 5, plan.TotalMatches)
	require.Equal(t, "y y\n", string(plan.Files[1].Content), "a file is replaced whole or not at all")
}

func TestUnit_Replace_RefusesBadQueries(t *testing.T) {
	root := tree(t, map[string]string{"a.txt": "x\n"})
	for name, q := range map[string]workspacereplace.Query{
		"empty":        {Pattern: ""},
		"bad regex":    {Pattern: "(", Regex: true},
		"bad glob":     {Pattern: "x", Include: []string{"["}},
		"escape":       {Pattern: "x", Path: "../elsewhere"},
		"missing path": {Pattern: "x", Path: "nope"},
		"skipped path": {Pattern: "x", Path: "vendor"},
		"too long":     {Pattern: string(make([]byte, 600))},
	} {
		_, err := workspacereplace.Build(context.Background(), root, q, skipVendor, workspacereplace.DefaultLimits())
		require.ErrorIs(t, err, workspacereplace.ErrInvalidQuery, name)
	}
}
