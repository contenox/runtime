package agentdecl

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var realTools = map[string]bool{
	"local_fs":        true,
	"local_shell":     true,
	"read_file":       true,
	"read_file_range": true,
	"write_file":      true,
	"edit_file":       true,

	"list_dir":    true,
	"find_files":  true,
	"stat_file":   true,
	"count_stats": true,

	"git_status":          true,
	"git_diff":            true,
	"git_log":             true,
	"git_show":            true,
	"git_blame":           true,
	"git_branch_list":     true,
	"git_add":             true,
	"git_commit":          true,
	"git_checkout_branch": true,
	"git_restore":         true,

	"mission_start":         true,
	"mission_plan":          true,
	"mission_report":        true,
	"mission_finish":        true,
	"mission_ask_attention": true,
	"mission_answer":        true,
	"mission_list":          true,
}

var promptProse = map[string]bool{
	"node_modules":         true,
	"main_rounds":          true,
	"recovery_rounds":      true,
	"rounds_used":          true,
	"recovery_rounds_used": true,
	"in_progress":          true,
}

var toolShapedToken = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)

var servedToolsets = map[string]bool{
	"local_fs":    true,
	"local_shell": true,
	"native-git":  true,
	"native-web":  true,
	"native-jq":   true,
	"native-go":   true,
	"native-goja": true,
	"native-ssh":  true,
	"native-echo": true,
	"mission":     true,
}

func preseedDeclarations(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := fs.WalkDir(preseedTrees, "preseed/agents", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		b, readErr := fs.ReadFile(preseedTrees, path)
		if readErr != nil {
			return readErr
		}
		files[path] = string(b)
		return nil
	})
	require.NoError(t, err)
	for _, p := range Preseeded {
		if strings.HasSuffix(p.RelPath, ".md") {
			files[p.RelPath] = p.Content()
		}
	}
	require.NotEmpty(t, files)
	return files
}

func declarationParts(content string) (frontmatter []string, body string) {
	front, rest, ok := splitFrontmatter([]byte(content))
	if !ok {
		return nil, content
	}
	return strings.Split(string(front), "\n"), string(rest)
}

func TestUnit_PreseedDeclarations_GrantsResolve(t *testing.T) {
	cfg, err := Shipped()
	require.NoError(t, err)
	for path, content := range preseedDeclarations(t) {
		frontmatter, _ := declarationParts(content)
		for _, line := range frontmatter {
			rest, ok := strings.CutPrefix(line, "tools:")
			if !ok {
				continue
			}
			rest = strings.Trim(strings.TrimSpace(rest), `"'`)
			names := strings.Split(rest, ",")
			mapped, skipped, err := cfg.MapTools(names)
			require.NoError(t, err, "%s: tools grant does not resolve", path)
			require.Empty(t, skipped, "%s: tools grant names dropped by the shipped mapping", path)
			require.NotEmpty(t, mapped, "%s: tools grant resolved to nothing", path)

			for _, m := range mapped {
				toolset, _, dotted := strings.Cut(m, ".")
				if !dotted {
					continue
				}
				require.True(t, servedToolsets[toolset],
					"%s: grant %q names toolset %q, which nothing serves", path, m, toolset)
			}
		}
	}
}

func TestUnit_PreseedDeclarations_PromptsNameNoPhantoms(t *testing.T) {
	for path, content := range preseedDeclarations(t) {
		_, body := declarationParts(content)
		for _, token := range toolShapedToken.FindAllString(body, -1) {
			if realTools[token] || promptProse[token] {
				continue
			}
			t.Errorf("%s: prompt names %q, which no shipped toolset serves", path, token)
		}
	}
}
