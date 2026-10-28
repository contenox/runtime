package acpsvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/stretchr/testify/require"
)

// TestUnit_ExplainDroppedContent_AbsenceIsTheSignal pins the rule that makes
// the envelope readable: a turn that dropped nothing emits no envelope, no
// notice, and no `_meta` at all, so a client can treat presence alone as "the
// prompt lost something" without decoding the payload.
func TestUnit_ExplainDroppedContent_AbsenceIsTheSignal(t *testing.T) {
	_, ok := explainDroppedContent(nil, nil)
	require.False(t, ok, "an intact prompt has nothing to explain")
	_, ok = explainDroppedContent([]string{}, nil)
	require.False(t, ok)

	_, announced := droppedContentNotice(nil, nil)
	require.False(t, announced, "an intact prompt announces nothing")

	bare := withDroppedContentMeta(libacp.PromptResponse{StopReason: libacp.StopReasonEndTurn}, nil, nil)
	require.Empty(t, bare.Meta, "an ordinary turn must stay bare on the wire")

	report, ok := explainDroppedContent([]string{string(libacp.ContentKindImage)}, nil)
	require.True(t, ok)
	require.Equal(t, []string{string(libacp.ContentKindImage)}, report.Kinds)
	require.Contains(t, report.Explanation, string(libacp.ContentKindImage))

	notice, announced := droppedContentNotice([]string{string(libacp.ContentKindImage)}, nil)
	require.True(t, announced)
	require.Equal(t, libacp.SessionUpdateAgentMessageChunk, notice.SessionUpdate)
	require.Contains(t, notice.Content.Text, string(libacp.ContentKindImage),
		"a client that renders only the message must still read what was lost")
	require.NotEmpty(t, notice.Meta, "and one that renders nothing custom must not be the only one served")
}

// TestUnit_WithDroppedContentMeta_KeepsSiblingMeta pins that attaching the
// report leaves other `_meta` entries of the response intact.
func TestUnit_WithDroppedContentMeta_KeepsSiblingMeta(t *testing.T) {
	resp := libacp.PromptResponse{
		StopReason: libacp.StopReasonEndTurn,
		Meta:       json.RawMessage(`{"other":"kept"}`),
	}
	both := withDroppedContentMeta(resp, []string{string(libacp.ContentKindImage)}, nil)

	var envelope struct {
		Other   string               `json:"other"`
		Dropped droppedContentReport `json:"contenox.droppedContent"`
	}
	require.NoError(t, json.Unmarshal(both.Meta, &envelope))
	require.Equal(t, "kept", envelope.Other)
	require.Equal(t, []string{string(libacp.ContentKindImage)}, envelope.Dropped.Kinds)

	require.Empty(t, withDroppedContentMeta(libacp.PromptResponse{}, nil, nil).Meta)
}

// TestLoopback_DroppedImage_ReachesTheClientOnTheWire is the regression for the
// incident: a prompt whose image could not be forwarded produced a normal,
// successful turn with the loss recorded only in the tracker, so a client with
// a camera button got an answer written as if no photo had been sent.
func TestLoopback_DroppedImage_ReachesTheClientOnTheWire(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.swapAgent(newResp.SessionID, &loopbackAgent{
		promptFunc: func(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
			return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
		},
	})

	// An undecodable image is the reachable shape of "the path could not carry
	// it": extractImageParts leaves it in the text blocks, where FlattenContent
	// reports it dropped.
	resp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []libacp.ContentBlock{
			libacp.NewTextContent("what is in this photo?"),
			libacp.NewImageContent("not-base64!!", "image/png"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, resp.StopReason)

	require.NotEmpty(t, resp.Meta, "a lossy turn must not answer like an intact one")
	var envelope map[string]droppedContentReport
	require.NoError(t, json.Unmarshal(resp.Meta, &envelope))
	require.Equal(t, []string{string(libacp.ContentKindImage)}, envelope[droppedContentMetaKey].Kinds)

	waitForAgentMessage(t, h, newResp.SessionID, string(libacp.ContentKindImage))

	intact, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("and now just text")},
	})
	require.NoError(t, err)
	require.Empty(t, intact.Meta, "a prompt that lost nothing carries no dropped-content marker")
}

// TestLoopback_SlashCommandDropsItsAttachment pins the command path, which
// discards an image that decoded perfectly well — a slash command is a text
// verb and has no use for it.
func TestLoopback_SlashCommandDropsItsAttachment(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	png := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a})
	resp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []libacp.ContentBlock{
			libacp.NewTextContent("/help"),
			libacp.NewImageContent(png, "image/png"),
		},
	})
	require.NoError(t, err)

	var envelope map[string]droppedContentReport
	require.NotEmpty(t, resp.Meta, "a command that threw the attachment away must say so")
	require.NoError(t, json.Unmarshal(resp.Meta, &envelope))
	require.Equal(t, []string{string(libacp.ContentKindImage)}, envelope[droppedContentMetaKey].Kinds)
}
