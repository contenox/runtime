package modelcontract_test

import (
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/stretchr/testify/require"
)

func TestUnit_NextEditParsesOnlyTheCapturedRegion(t *testing.T) {
	before := "const café = 1;\r\nconsole.log(café);\r\n"
	current := "const value = 1;\r\nconsole.log(café);\r\n"
	wanted := "const value = 1;\r\nconsole.log(value);\r\n"
	input := modelcontract.NextEditInput{Path: "src/file.ts", Before: before, Current: current, Cursor: len("const value"), Start: 0, End: len(current)}
	for _, format := range []string{"sweep-v1", "sweep-v2", "zeta-2.1"} {
		t.Run(format, func(t *testing.T) {
			prepared, err := modelcontract.PrepareNextEdit(format, input)
			require.NoError(t, err)
			output := strings.TrimPrefix(wanted, prepared.Prefill)
			if format == "zeta-2.1" {
				output = "<|marker_1|>" + wanted + "<|marker_2|>\n"
			}
			edits, err := prepared.Parse(output, "stop")
			require.NoError(t, err)
			require.Len(t, edits, 1)
			require.NoError(t, modelcontract.ValidateEdits(input, edits))
			edit := edits[0]
			require.Equal(t, wanted, current[:edit.Start]+edit.NewText+current[edit.End:])
			require.Equal(t, "café", edit.OldText)
			_, err = prepared.Parse(output, "length")
			require.ErrorContains(t, err, "complete")
			stale := input
			stale.Current = strings.ReplaceAll(current, "café", "oops")
			stale.End = len(stale.Current)
			require.Error(t, modelcontract.ValidateEdits(stale, edits))
		})
	}
}

func TestUnit_NextEditProfilesFollowPublisherFraming(t *testing.T) {
	before := "def hello():\n    return 1\n\nprint(hello())\n"
	current := strings.Replace(before, "def hello", "def greet", 1)
	input := modelcontract.NextEditInput{Path: "greet.py", Before: before, Current: current, Cursor: strings.Index(current, "print("), End: len(current)}
	sweep, err := modelcontract.PrepareNextEdit("sweep-v2", input)
	require.NoError(t, err)
	require.Contains(t, sweep.Render(), "<|file_sep|>greet.py:1:1\noriginal:\ndef hello():\n\nupdated:\ndef greet():\n")
	require.Contains(t, sweep.Render(), "<|file_sep|>original/greet.py:1:4\n"+current)
	require.Contains(t, sweep.Render(), "<|cursor|>print(hello())")
	zeta, err := modelcontract.PrepareNextEdit("zeta-2.1", input)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(zeta.Render(), "<[fim-suffix]>\n<[fim-prefix]>"))
	require.Contains(t, zeta.Render(), "@@ -1,1 +1,1 @@\n-def hello():\n+def greet():\n")
	require.Contains(t, zeta.Render(), "<|marker_1|>def greet():")
	require.Contains(t, zeta.Render(), "<|marker_2|><|marker_3|>\n<[fim-middle]>\n")
	output := "<|marker_1|>" + strings.Replace(current, "print(hello())", "print(greet<|user_cursor|>())", 1) + "<|marker_2|>\n"
	edits, err := zeta.Parse(output, "stop")
	require.NoError(t, err)
	require.Equal(t, []modelcontract.TextEdit{{Start: strings.Index(current, "hello"), End: strings.Index(current, "hello") + len("hello"), OldText: "hello", NewText: "greet"}}, edits)
	_, err = (modelcontract.PreparedEdit{}).Parse("anything", "stop")
	require.Error(t, err)
}

func TestUnit_NextEditRejectsInvalidPositionsAndMalformedEnvelope(t *testing.T) {
	input := modelcontract.NextEditInput{Path: "a.py", Before: "π = 1\n", Current: "π = 2\n", Cursor: 1, End: len("π = 2\n")}
	require.ErrorContains(t, input.Validate(), "UTF-8 boundaries")
	input.Cursor = 2
	prepared, err := modelcontract.PrepareNextEdit("zeta-2.1", input)
	require.NoError(t, err)
	_, err = prepared.Parse("```python\nπ = 3\n```", "stop")
	require.ErrorContains(t, err, "envelope")
	_, err = prepared.Parse("<|marker_1|>\nπ = 3\n<|marker_2|>\nextra", "stop")
	require.ErrorContains(t, err, "envelope")
	input.Start = 2
	require.Error(t, modelcontract.ValidateEdits(input, []modelcontract.TextEdit{{Start: 0, End: 2, OldText: "π", NewText: "x"}}))
}

func TestUnit_NextEditNoChangeAndLiteralSourceArePreserved(t *testing.T) {
	input := modelcontract.NextEditInput{Path: "test.py", Before: "x = ''", Current: "x = '<|file_sep|>'", End: len("x = '<|file_sep|>'")}
	prepared, err := modelcontract.PrepareNextEdit("sweep-v1", input)
	require.NoError(t, err)
	var literal string
	_, _, err = modelcontract.TokenizeParts(prepared.Parts, prepared.Stops, func(text string, special bool) ([]int, error) {
		if !special {
			literal += text
		}
		return []int{1}, nil
	})
	require.NoError(t, err)
	require.Contains(t, literal, input.Current)
	edits, err := prepared.Parse(input.Current, "stop")
	require.NoError(t, err)
	require.Empty(t, edits)
	input.Before = ""
	input.Start = 1
	input.Cursor = 1
	_, err = modelcontract.PrepareNextEdit("sweep-v2", input)
	require.NoError(t, err)
}
