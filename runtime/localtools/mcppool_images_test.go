package localtools

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestUnit_MCPCollectImages_KeepsImageBlocksAndText(t *testing.T) {
	contents := []mcp.Content{
		&mcp.TextContent{Text: "Took a screenshot"},
		&mcp.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		&mcp.ImageContent{MIMEType: "image/jpeg", Data: nil},
	}
	images := mcpCollectImages(contents)
	require.Len(t, images, 1)
	require.Equal(t, "image/png", images[0].MimeType)
	require.Equal(t, "Took a screenshot", mcpCollectText(contents))
}

func TestUnit_DecodeMCPImageResult_SurvivesTheBusRoundTrip(t *testing.T) {
	wire, err := json.Marshal(&MCPImageResult{Kind: MCPImageResultKind, Text: "shot", Images: []MCPImage{{MimeType: "image/png", Data: []byte{9, 8, 7}}}})
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(wire, &generic))

	decoded, ok := DecodeMCPImageResult(generic)
	require.True(t, ok)
	require.Equal(t, "shot", decoded.Text)
	require.Len(t, decoded.Images, 1)
	require.Equal(t, []byte{9, 8, 7}, decoded.Images[0].Data)

	_, ok = DecodeMCPImageResult(map[string]any{"kind": "other", "text": "x"})
	require.False(t, ok)
	_, ok = DecodeMCPImageResult("plain text")
	require.False(t, ok)
}
