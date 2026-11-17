package acpsvc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

var quotedCommand = regexp.MustCompile("`(/[^`]+)`")

func phrases(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "../../internal/remedy/remedy.go", nil, 0)
	require.NoError(t, err)
	var out []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		require.NoError(t, err)
		out = append(out, value)
		return true
	})
	require.NotEmpty(t, out)
	return out
}

func normalizedArguments(fields []string) string {
	backends, roles := []string{"llama", "openvino"}, []string{"primary", "alternative", "autocomplete", "next-edit", "embedding"}
	normalized := make([]string, len(fields))
	for index, field := range fields {
		switch {
		case field == "<"+strings.Join(backends, "|")+">" || slices.Contains(backends, field):
			normalized[index] = "<backend>"
		case slices.Contains(roles, field):
			normalized[index] = "<role>"
		default:
			normalized[index] = field
		}
	}
	return strings.Join(normalized, " ")
}

func requireDeclaredCommands(t *testing.T, phrase string) int {
	t.Helper()
	declared := map[string]libacp.AvailableCommand{}
	for _, command := range allACPCommands() {
		declared[command.Name] = command
	}
	matches := quotedCommand.FindAllStringSubmatch(phrase, -1)
	for _, match := range matches {
		fields := strings.Fields(strings.TrimPrefix(match[1], "/"))
		command, ok := declared[fields[0]]
		require.True(t, ok, "%s names a slash command the catalog does not declare", match[1])
		arguments := normalizedArguments(fields[1:])
		if arguments != "" {
			require.NotNil(t, command.Input)
			require.Contains(t, normalizedArguments(strings.Fields(command.Input.Hint)), arguments, "%s does not match the declared usage of /%s", match[1], command.Name)
		}
	}
	return len(matches)
}

func TestUnit_RemedyQuotedSlashCommandsAreDeclared(t *testing.T) {
	quoted := 0
	for _, phrase := range phrases(t) {
		quoted += requireDeclaredCommands(t, phrase)
	}
	require.NotZero(t, quoted)
}

func TestUnit_RemedyPullAndAssignQuotesDeclaredCommands(t *testing.T) {
	phrase := remedy.PullAndAssign("<model>", "<role>", "<connection>")
	require.Equal(t, 2, requireDeclaredCommands(t, phrase))
	require.NotContains(t, phrase, "%")
}

func TestUnit_RemedyRaiseCeilingQuotesTheDeclaredCommand(t *testing.T) {
	phrase := remedy.RaiseCeiling("<pool>")
	require.Equal(t, 1, requireDeclaredCommands(t, phrase))
	require.Contains(t, phrase, "raise the ceiling for <pool>")
}

func TestUnit_RemedyPhrasesCarryNoFormatVerb(t *testing.T) {
	for _, phrase := range phrases(t) {
		require.NotContains(t, phrase, "%", phrase)
		require.False(t, strings.HasSuffix(phrase, "."), phrase)
	}
}
