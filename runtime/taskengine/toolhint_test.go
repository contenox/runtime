package taskengine

import (
	"strings"
	"testing"
)

func toolContext(names ...string) *ChainContext {
	tools := make(map[string]ToolWithResolution, len(names))
	for _, name := range names {
		tools[name] = ToolWithResolution{
			Tool:      Tool{Type: "function", Function: FunctionTool{Name: name}},
			ToolsName: name,
		}
	}
	return &ChainContext{Tools: tools}
}

func TestUnit_ToolNotFoundSuggestion_LeafMatchWins(t *testing.T) {
	ctx := toolContext(
		"local_fs.read_file",
		"local_fs.stat_file",
		"local_fs.list_dir",
		"native-git.git_status",
	)

	got := toolNotFoundSuggestion(ctx, nil, "local_fs.browse.stat_file")
	if want := "; did you mean local_fs.stat_file,"; !strings.HasPrefix(got, want) {
		t.Fatalf("suggestion = %q, want it to lead with %q", got, want)
	}
}

func TestUnit_ToolNotFoundSuggestion_SharedNamespaceToken(t *testing.T) {
	ctx := toolContext(
		"local_fs.list_dir",
		"local_fs.find_files",
		"native-git.git_status",
		"webtools.fetch",
	)

	got := toolNotFoundSuggestion(ctx, nil, "local_fs.browse")
	if want := "; did you mean local_fs.find_files, local_fs.list_dir?"; got != want {
		t.Fatalf("suggestion = %q, want %q", got, want)
	}
}

func TestUnit_ToolNotFoundSuggestion_UnrelatedNameListsToolsets(t *testing.T) {
	ctx := toolContext("native-git.git_status", "webtools.fetch")

	got := toolNotFoundSuggestion(ctx, nil, "shell.run")
	if want := "; available toolsets: native-git, webtools"; got != want {
		t.Fatalf("suggestion = %q, want %q", got, want)
	}
}

func TestUnit_ToolNotFoundSuggestion_NeverSuggestsHiddenOrItself(t *testing.T) {
	ctx := toolContext("local_fs.stat_file", "local_fs.list_dir")
	hidden := map[string]struct{}{"local_fs.list_dir": {}}

	got := toolNotFoundSuggestion(ctx, hidden, "local_fs.stat_file")
	if got != "" {
		t.Fatalf("suggestion = %q, want none: the only other tool is hidden and the failed name is itself a real one", got)
	}
	if got := toolNotFoundSuggestion(ctx, nil, "local_fs.stat_file"); got != "; did you mean local_fs.list_dir?" {
		t.Fatalf("suggestion = %q", got)
	}
}

func TestUnit_ToolNotFoundSuggestion_EmptyContextStaysQuiet(t *testing.T) {
	if got := toolNotFoundSuggestion(nil, nil, "anything"); got != "" {
		t.Fatalf("suggestion = %q, want empty", got)
	}
	if got := toolNotFoundSuggestion(&ChainContext{}, nil, "anything"); got != "" {
		t.Fatalf("suggestion = %q, want empty", got)
	}
}
