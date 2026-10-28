package acpsvc

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
)

// droppedContentMetaKey namespaces the dropped-prompt-content report carried on
// a prompt response's `_meta`, a sibling of contenox.stopReason.
const droppedContentMetaKey = "contenox.droppedContent"

// droppedContentReport is the operator-facing form of the prompt content that
// never reached the model.
type droppedContentReport struct {
	Kinds       []string `json:"kinds"`
	Explanation string   `json:"explanation"`
}

// droppedReasons names, per discarded content kind, why it never reached the
// model. A kind without a reason gets the generic sentence.
type droppedReasons map[string]string

// explainDroppedContent maps a set of discarded content kinds onto its report;
// ok is false for an empty set. Each kind is explained once: by the reason the
// turn established for it, by the audio acceptance sentence for audio dropped
// without one, and by the generic sentence only for the kinds no reason covers.
func explainDroppedContent(kinds []string, reasons droppedReasons) (droppedContentReport, bool) {
	if len(kinds) == 0 {
		return droppedContentReport{}, false
	}
	explanation := "Part of the prompt never reached the model and had no effect on the answer: " +
		strings.Join(kinds, ", ") + ". The turn ran on the rest of the prompt alone."
	var unexplained []string
	for _, kind := range kinds {
		switch {
		case strings.TrimSpace(reasons[kind]) != "":
			explanation += " " + strings.TrimSpace(reasons[kind])
		case kind == string(libacp.ContentKindAudio):
			explanation += " " + audioAcceptanceSentence()
		default:
			unexplained = append(unexplained, kind)
		}
	}
	if len(unexplained) > 0 {
		explanation += " Either this path takes text only (a slash command does), the attachment could not be decoded, or the session's model does not accept it — so the reply will keep ignoring it until it is resent somewhere that does."
	}
	return droppedContentReport{Kinds: kinds, Explanation: explanation}, true
}

func appendDroppedKind(kinds []string, kind string) []string {
	if slices.Contains(kinds, kind) {
		return kinds
	}
	return append(kinds, kind)
}

func droppedContentMessage(r droppedContentReport) string {
	return "⚠️  " + r.Explanation
}

// droppedContentNotice is the agent message a turn with discarded content
// announces into the conversation; ok is false when nothing was dropped.
func droppedContentNotice(kinds []string, reasons droppedReasons) (libacp.SessionUpdate, bool) {
	report, ok := explainDroppedContent(kinds, reasons)
	if !ok {
		return libacp.SessionUpdate{}, false
	}
	update := libacp.NewAgentMessageChunk(droppedContentMessage(report))
	update.Meta = mergeMeta(nil, droppedContentMetaKey, report)
	return update, true
}

// announceDroppedContent pushes the notice into the session through emit; a
// no-op when nothing was dropped.
func announceDroppedContent(ctx context.Context, sid libacp.SessionID, kinds []string, reasons droppedReasons, emit func(context.Context, libacp.SessionNotification)) {
	notice, ok := droppedContentNotice(kinds, reasons)
	if !ok || emit == nil {
		return
	}
	emit(ctx, libacp.SessionNotification{SessionID: sid, Update: notice})
}

// withDroppedContentMeta attaches the report to a prompt response's `_meta`,
// preserving anything already there, and returns resp untouched when nothing was
// dropped.
func withDroppedContentMeta(resp libacp.PromptResponse, kinds []string, reasons droppedReasons) libacp.PromptResponse {
	report, ok := explainDroppedContent(kinds, reasons)
	if !ok {
		return resp
	}
	resp.Meta = mergeMeta(resp.Meta, droppedContentMetaKey, report)
	return resp
}

// mergeMeta sets key on an ACP `_meta` object without disturbing its other
// entries, returning meta unchanged when value cannot be marshalled or meta is
// not a JSON object.
func mergeMeta(meta json.RawMessage, key string, value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return meta
	}
	envelope := map[string]json.RawMessage{}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &envelope); err != nil {
			return meta
		}
	}
	envelope[key] = encoded
	merged, err := json.Marshal(envelope)
	if err != nil {
		return meta
	}
	return merged
}
