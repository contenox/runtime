package bedrock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func runFilter(deltas []string) (thinking, answer string) {
	f := &thinkingTagFilter{}
	for _, d := range deltas {
		th, an := f.feed(d)
		thinking += th
		answer += an
	}
	th, an := f.flush()
	return thinking + th, answer + an
}

func TestUnit_ThinkingTagFilter_EverySplitPointYieldsTheSameResult(t *testing.T) {
	sample := "\n<thinking> I need to read the README first. </thinking>\n\nThe project builds agents."
	for i := 0; i <= len(sample); i++ {
		for j := i; j <= len(sample); j++ {
			thinking, answer := runFilter([]string{sample[:i], sample[i:j], sample[j:]})
			require.Equal(t, " I need to read the README first. ", thinking, "split %d/%d", i, j)
			require.Equal(t, "The project builds agents.", answer, "split %d/%d", i, j)
		}
	}
}

func TestUnit_ThinkingTagFilter_TextWithoutTheTagPassesThroughUntouched(t *testing.T) {
	thinking, answer := runFilter([]string{"<th", "e answer is 42>", " really"})
	require.Equal(t, "", thinking)
	require.Equal(t, "<the answer is 42> really", answer)

	thinking, answer = runFilter([]string{"Plain ", "text </thinking> kept"})
	require.Equal(t, "", thinking)
	require.Equal(t, "Plain text </thinking> kept", answer)

	thinking, answer = runFilter([]string{"<thin"})
	require.Equal(t, "", thinking)
	require.Equal(t, "<thin", answer, "an unfinished opening tag at the end is still answer text")
}

func TestUnit_ThinkingTagFilter_UnclosedTagStaysThinking(t *testing.T) {
	thinking, answer := runFilter([]string{"<thinking>", "never ", "closed"})
	require.Equal(t, "never closed", thinking)
	require.Equal(t, "", answer)
}

func TestUnit_SplitLeadingThinking(t *testing.T) {
	thinking, answer, ok := splitLeadingThinking("  <thinking>\nplan\n</thinking>\n\nanswer")
	require.True(t, ok)
	require.Equal(t, "plan", thinking)
	require.Equal(t, "answer", answer)

	thinking, answer, ok = splitLeadingThinking("answer <thinking>late</thinking>")
	require.False(t, ok)
	require.Equal(t, "", thinking)
	require.Equal(t, "answer <thinking>late</thinking>", answer)

	thinking, answer, ok = splitLeadingThinking("<thinking>cut off")
	require.True(t, ok)
	require.Equal(t, "cut off", thinking)
	require.Equal(t, "", answer)
}
