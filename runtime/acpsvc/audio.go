package acpsvc

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/taskengine"
)

// extractAudioParts splits content blocks into audio attachments and the rest.
// A block outside modelrepo's media-type or inline-size bounds is returned in
// rest, so the loss stays per block instead of failing the whole turn.
func extractAudioParts(blocks []libacp.ContentBlock) (audio []taskengine.AudioPart, rest []libacp.ContentBlock) {
	total := 0
	for _, block := range blocks {
		if block.Type != string(libacp.ContentKindAudio) {
			rest = append(rest, block)
			continue
		}
		data, err := base64.StdEncoding.DecodeString(block.Data)
		if err != nil || len(data) == 0 {
			rest = append(rest, block)
			continue
		}
		if !modelrepo.SupportedAudioMimeTypes[block.MimeType] || total+len(data) > modelrepo.MaxInlineAudioBytes {
			rest = append(rest, block)
			continue
		}
		total += len(data)
		audio = append(audio, taskengine.AudioPart{Data: data, MimeType: block.MimeType})
	}
	return audio, rest
}

// audioAcceptanceSentence names what inline audio needs to survive this surface,
// rendered from the modelrepo constants so the copy cannot drift.
func audioAcceptanceSentence() string {
	types := make([]string, 0, len(modelrepo.SupportedAudioMimeTypes))
	for t := range modelrepo.SupportedAudioMimeTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return fmt.Sprintf("Audio is accepted as %s, up to %d MiB of decoded audio per prompt, and needs an audio-capable model (the default-audio-model config key sets one); audio outside those bounds, or audio for a session without such a model, is refused here, before the model.",
		strings.Join(types, ", "), modelrepo.MaxInlineAudioBytes>>20)
}

// sessionAudioRefusal is the pre-flight capability gate for a prompt carrying
// audio. It returns the operator-facing reason, or "" to forward the audio.
func (t *Transport) sessionAudioRefusal(ctx context.Context, sess *sessionEntry) string {
	return audioCapabilityRefusal(t.runtimeStates(ctx), sess.providerOrDefault(t.provider()), sess.modelOrDefault(t.model()))
}

// audioCapabilityRefusal refuses only on positive knowledge: a selected model
// the catalog reports as non-audio, or a catalog with offerable models and none
// audio-capable. Anything unknown forwards, leaving the resolver the final
// word. The session's promptCapabilities answer from the same verdict.
func audioCapabilityRefusal(states []runtimestate.BackendRuntimeState, provider, model string) string {
	switch selectionMediaVerdict(states, provider, model, canAudio) {
	case mediaRefusedByModel:
		return fmt.Sprintf("The session's model %q does not accept audio input, so the audio was not sent and the turn ran without it. Select an audio-capable model for this session, or set the default-audio-model config key, then resend the audio.", model)
	case mediaRefusedByCatalog:
		return "No configured model accepts audio input, so the audio was not sent and the turn ran without it. Configure an audio-capable model (the default-audio-model config key sets one), then resend the audio."
	}
	return ""
}
