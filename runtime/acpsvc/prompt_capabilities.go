package acpsvc

import (
	"context"
	"fmt"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
)

// mediaVerdict is what the catalog knows about one prompt medium for one
// selection. Only positive knowledge refuses: an empty catalog, or a model it
// does not list over a catalog where some model accepts the medium, forwards.
type mediaVerdict int

const (
	mediaForwarded mediaVerdict = iota
	mediaRefusedByModel
	mediaRefusedByCatalog
)

func canVision(m runtimestate.ModelPullStatus) bool { return m.CanVision }

func canAudio(m runtimestate.ModelPullStatus) bool { return m.CanAudio }

// catalogMediaVerdict answers for the catalog as a whole: refused only when it
// lists offerable models and none accepts the medium.
func catalogMediaVerdict(states []runtimestate.BackendRuntimeState, accepts func(runtimestate.ModelPullStatus) bool) mediaVerdict {
	listed := false
	for _, state := range states {
		for _, model := range offerableModels(state.PulledModels) {
			listed = true
			if accepts(model) {
				return mediaForwarded
			}
		}
	}
	if listed {
		return mediaRefusedByCatalog
	}
	return mediaForwarded
}

// selectionMediaVerdict answers for the provider and model a session selected,
// matching the catalog the way the reasoning option does; an empty provider
// matches every backend. A selection the catalog does not list takes the
// catalog's answer.
func selectionMediaVerdict(states []runtimestate.BackendRuntimeState, provider, model string, accepts func(runtimestate.ModelPullStatus) bool) mediaVerdict {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	listed, accepted := false, false
	if model != "" {
		for _, state := range states {
			if provider != "" && !runtimestate.MatchesBackend(provider, state.Backend) {
				continue
			}
			for _, pulled := range state.PulledModels {
				if !modelrepo.SameModel(pulled.Model, model) && !modelrepo.SameModel(pulled.Name, model) {
					continue
				}
				listed = true
				accepted = accepted || accepts(pulled)
			}
		}
	}
	switch {
	case listed && accepted:
		return mediaForwarded
	case listed:
		return mediaRefusedByModel
	}
	return catalogMediaVerdict(states, accepts)
}

// catalogPromptCapabilities is what initialize advertises: a medium any
// offerable model accepts, or every medium while the catalog is still empty.
// A prompt the session's model cannot take is dropped and reported per turn. Embedded context is flattened into the prompt text,
// which every model takes.
func (t *Transport) catalogPromptCapabilities(ctx context.Context) libacp.PromptCapabilities {
	states := t.runtimeStates(ctx)
	return libacp.PromptCapabilities{
		Image:           catalogMediaVerdict(states, canVision) == mediaForwarded,
		Audio:           catalogMediaVerdict(states, canAudio) == mediaForwarded,
		EmbeddedContext: true,
	}
}

// sessionImageRefusal is the pre-flight capability gate for a prompt carrying
// images. It returns the operator-facing reason, or "" to forward the images.
func (t *Transport) sessionImageRefusal(ctx context.Context, sess *sessionEntry) string {
	return imageCapabilityRefusal(t.runtimeStates(ctx), sess.providerOrDefault(t.provider()), sess.modelOrDefault(t.model()))
}

func imageCapabilityRefusal(states []runtimestate.BackendRuntimeState, provider, model string) string {
	switch selectionMediaVerdict(states, provider, model, canVision) {
	case mediaRefusedByModel:
		return fmt.Sprintf("The session's model %q does not accept images, so the image was not sent and the turn ran without it. Select a vision-capable model for this session, then resend the image.", model)
	case mediaRefusedByCatalog:
		return "No configured model accepts images, so the image was not sent and the turn ran without it. Configure a vision-capable model, then resend the image."
	}
	return ""
}

// offerableModels returns the models a backend contributes to a session: the
// chat- or prompt-capable ones, or, when it reports none that way, the ones it
// does not describe at all (silence is not an answer).
func offerableModels(models []runtimestate.ModelPullStatus) []runtimestate.ModelPullStatus {
	capable := make([]runtimestate.ModelPullStatus, 0, len(models))
	undescribed := make([]runtimestate.ModelPullStatus, 0, len(models))
	for _, model := range models {
		if model.CanChat || model.CanPrompt {
			capable = append(capable, model)
			continue
		}
		if !model.CanEmbed && !model.CanStream && !model.CanThink && !model.CanVision && !model.CanAudio {
			undescribed = append(undescribed, model)
		}
	}
	if len(capable) > 0 {
		return capable
	}
	return undescribed
}
