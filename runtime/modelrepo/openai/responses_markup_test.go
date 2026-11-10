package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnit_StripInlineToolCallMarkup(t *testing.T) {
	leaked := "Die Namen sind jetzt eindeutig. to=functions.local_shell_local_shell (json \u29eb {\"command\":\"go\",\"args\":[\"test\",\"./...\"],\"cwd\":\"C:\\\\x\",\"timeout\":\"120s\"} to=functions.local_shell_local_shell (json code: {\"command\":\"go\",\"args\":[\"test\",\"./...\"],\"cwd\":\"C:\\\\x\",\"timeout\":\"120s\"})Ich habe null.go geprüft."
	assert.Equal(t, "Die Namen sind jetzt eindeutig.Ich habe null.go geprüft.", stripInlineToolCallMarkup(leaked))
	assert.Equal(t, "plain answer", stripInlineToolCallMarkup("plain answer"))
	assert.Equal(t, "see functions.md for details", stripInlineToolCallMarkup("see functions.md for details"))
}
