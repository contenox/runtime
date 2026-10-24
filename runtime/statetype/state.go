package statetype

import (
	"strings"

	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/ollama/ollama/api"
	ollamamodel "github.com/ollama/ollama/types/model"
)

type BackendRuntimeState = runtimestate.BackendRuntimeState

type ModelPullStatus = runtimestate.ModelPullStatus

type ModelDetails = runtimestate.ModelDetails

func EnrichFromOllamaShow(m *ModelPullStatus, r *api.ShowResponse) {
	for _, cap := range r.Capabilities {
		switch cap {
		case ollamamodel.CapabilityCompletion:
			m.CanChat = true
			m.CanPrompt = true
			m.CanStream = true
		case ollamamodel.CapabilityEmbedding:
			m.CanEmbed = true
		case ollamamodel.CapabilityTools:
			m.CanChat = true
		}
	}
	if m.ContextLength == 0 {
		for k, v := range r.ModelInfo {
			if strings.HasSuffix(k, ".context_length") {
				if n, ok := v.(float64); ok {
					m.ContextLength = int(n)
				}
				break
			}
		}
	}
}

func ConvertOllamaModelResponse(model *api.ListModelResponse) *ModelPullStatus {
	list := &ModelPullStatus{
		Name:       model.Name,
		Model:      model.Model,
		ModifiedAt: model.ModifiedAt,
		Size:       model.Size,
		Digest:     model.Digest,
		Details: ModelDetails{
			ParentModel:       model.Details.ParentModel,
			Format:            model.Details.Format,
			Family:            model.Details.Family,
			Families:          model.Details.Families,
			ParameterSize:     model.Details.ParameterSize,
			QuantizationLevel: model.Details.QuantizationLevel,
		},
	}
	return list
}

var _ = runtimetypes.Backend{}
