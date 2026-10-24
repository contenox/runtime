package modelcontract

import (
	"fmt"
	"strings"
)

func sourceContext(input NextEditInput) (int, int) {
	start, end := input.Start, input.End
	for line := 0; line < 150 && start > 0; line++ {
		previous := strings.LastIndexByte(input.Current[:start-1], '\n') + 1
		if input.Start-previous > 4096 {
			break
		}
		start = previous
	}
	for line := 0; line < 150 && end < len(input.Current); line++ {
		next := strings.IndexByte(input.Current[end:], '\n')
		if next < 0 {
			next = len(input.Current) - end - 1
		}
		if end+next+1-input.End > 4096 {
			break
		}
		end += next + 1
	}
	return start, end
}

func lineNumber(text string, offset int) int {
	return strings.Count(text[:offset], "\n") + 1
}

func lastLineNumber(text string, end int) int {
	if end > 0 && text[end-1] == '\n' {
		end--
	}
	return lineNumber(text, end)
}

func editHistoryRange(change EditHistory) (int, int, int) {
	start, beforeEnd, afterEnd := changedRange(change.Before, change.After)
	start = strings.LastIndexByte(change.Before[:start], '\n') + 1
	next := strings.IndexByte(change.Before[beforeEnd:], '\n')
	if next < 0 {
		next = len(change.Before) - beforeEnd - 1
	}
	beforeEnd += next + 1
	next = strings.IndexByte(change.After[afterEnd:], '\n')
	if next < 0 {
		next = len(change.After) - afterEnd - 1
	}
	afterEnd += next + 1
	return start, beforeEnd, afterEnd
}

func editHistoryDiff(change EditHistory) string {
	if change.Before == change.After {
		return ""
	}
	start, beforeEnd, afterEnd := editHistoryRange(change)
	before, after := change.Before[start:beforeEnd], change.After[start:afterEnd]
	countLines := func(text string) int {
		if text == "" {
			return 0
		}
		return lastLineNumber(text, len(text))
	}
	var text strings.Builder
	fmt.Fprintf(&text, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", change.Path, change.Path,
		lineNumber(change.Before, start), countLines(before), lineNumber(change.After, start), countLines(after))
	for _, side := range []struct{ prefix, source string }{{"-", before}, {"+", after}} {
		for _, line := range strings.SplitAfter(side.source, "\n") {
			if line == "" {
				continue
			}
			text.WriteString(side.prefix + line)
			if !strings.HasSuffix(line, "\n") {
				text.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return text.String()
}
