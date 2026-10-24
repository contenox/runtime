package runtimestate

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/ollama"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type BackendRuntimeState struct {
	DiscoveryError   string               `json:"discoveryError,omitempty"`
	ID               string               `json:"id" example:"b7d9e1a3-8f0c-4a7d-9b1e-2f3a4b5c6d7e"`
	Name             string               `json:"name" example:"ollama-production"`
	Models           []string             `json:"models" example:"[\"llama2:7b\", \"qwen2.5:7b\", \"nomic-embed-text:latest\"]"`
	PulledModels     []ModelPullStatus    `json:"pulledModels"`
	Backend          runtimetypes.Backend `json:"backend"`
	Error            string               `json:"error,omitempty" example:"connection timeout: context deadline exceeded"`
	ResolvedEndpoint string               `json:"resolvedEndpoint,omitempty"`
	ResolvedInstance string               `json:"resolvedInstance,omitempty"`
	LiveEngine       string               `json:"liveEngine,omitempty"`
	apiKey           string
	authorize        func(context.Context) (http.Header, error)
}

type ModelPullStatus struct {
	FIMFormat              string                         `json:"fimFormat,omitempty"`
	NextEditFormat         string                         `json:"nextEditFormat,omitempty"`
	CanAutocomplete        bool                           `json:"canAutocomplete"`
	CanNextEdit            bool                           `json:"canNextEdit"`
	Registered             bool                           `json:"registered"`
	AdvertisedCapabilities []string                       `json:"advertisedCapabilities"`
	AdapterCapabilities    []string                       `json:"adapterCapabilities"`
	NativeBackend          string                         `json:"nativeBackend,omitempty"`
	CompletionMode         string                         `json:"completionMode,omitempty"`
	Name                   string                         `json:"name" example:"Llama 2 7B"`
	Model                  string                         `json:"model" example:"llama2:7b"`
	ModifiedAt             time.Time                      `json:"modifiedAt" example:"2023-11-15T14:30:45Z"`
	Size                   int64                          `json:"size" example:"4709611008"`
	Digest                 string                         `json:"digest" example:"sha256:9e3a6c0d3b5e7f8a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a"`
	Details                ModelDetails                   `json:"details"`
	ContextLength          int                            `json:"contextLength" example:"4096"`
	MaxOutputTokens        int                            `json:"maxOutputTokens,omitempty" example:"8192"`
	CanChat                bool                           `json:"canChat" example:"true"`
	CanEmbed               bool                           `json:"canEmbed" example:"false"`
	CanPrompt              bool                           `json:"canPrompt" example:"true"`
	CanStream              bool                           `json:"canStream" example:"true"`
	CanThink               bool                           `json:"canThink,omitempty" example:"true"`
	CanVision              bool                           `json:"canVision,omitempty" example:"true"`
	CanAudio               bool                           `json:"canAudio,omitempty" example:"true"`
	AudioExtension         bool                           `json:"audioExtension,omitempty"`
	SessionExtension       bool                           `json:"sessionExtension,omitempty"`
	ThinkingTypes          *modelrepo.ThinkingTypes       `json:"thinkingTypes,omitempty"`
	PromptCache            *modelrepo.PromptCacheContract `json:"promptCache,omitempty"`
	Pricing                *runtimetypes.ModelPricing     `json:"pricing,omitempty"`
	DeclaredCapabilities   []string                       `json:"declaredCapabilities,omitempty"`
}

type ModelDetails struct {
	ParentModel       string   `json:"parentModel" example:"llama2:7b"`
	Format            string   `json:"format" example:"gguf"`
	Family            string   `json:"family" example:"llama"`
	Families          []string `json:"families" example:"[\"llama\", \"7B\"]"`
	ParameterSize     string   `json:"parameterSize" example:"7B"`
	QuantizationLevel string   `json:"quantizationLevel" example:"Q4_K_M"`
}

func (s *BackendRuntimeState) GetAPIKey() string {
	return s.apiKey
}

func (s *BackendRuntimeState) SetAPIKey(key string) {
	s.apiKey = key
}

func EnrichFromOllamaShow(m *ModelPullStatus, r *ollama.ShowResponse) {
	for _, cap := range r.Capabilities {
		switch cap {
		case ollama.CapabilityCompletion:
			m.CanChat = true
			m.CanPrompt = true
			m.CanStream = true
		case ollama.CapabilityEmbedding:
			m.CanEmbed = true
		case ollama.CapabilityTools:
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

func ConvertOllamaModelResponse(model *ollama.ListModelResponse) *ModelPullStatus {
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
