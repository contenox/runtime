package modelregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

var ErrNotFound = errors.New("model not found in registry")

type ModelDescriptor struct {
	ID                string `json:"id,omitempty" example:"m1a2b3c4-d5e6-f7g8-h9i0-j1k2l3m4n5o6"`
	Name              string `json:"name" example:"qwen3-8b"`
	SourceURL         string `json:"sourceUrl" example:"https://huggingface.co/Qwen/..."`
	SizeBytes         int64  `json:"sizeBytes" example:"934000000"`
	Curated           bool   `json:"curated" example:"true"`
	Backend           string `json:"backend,omitempty" example:"openvino"`
	Repo              string `json:"repo,omitempty" example:"OpenVINO/Qwen3-8B-int4-ov"`
	ToolProtocol      string `json:"toolProtocol,omitempty" example:"llama:common_chat_tool_parser"`
	ReasoningProtocol string `json:"reasoningProtocol,omitempty" example:"llama:common_chat_reasoning_parser"`
	ReasoningFormat   string `json:"reasoningFormat,omitempty" example:"deepseek"`
	Family            string `json:"family,omitempty" example:"qwen3"`
	DisplayLabel      string `json:"displayLabel,omitempty" example:"Qwen 3 8B"`
	UseCase           string `json:"useCase,omitempty" example:"coding"`
	RecommendedVRAMGB int    `json:"recommendedVramGb,omitempty" example:"8"`
	Vision            bool   `json:"vision,omitempty" example:"true"`
	MMProjURL         string `json:"mmprojUrl,omitempty" example:"https://huggingface.co/.../mmproj-....gguf"`
	MMProjSizeBytes   int64  `json:"mmprojSizeBytes,omitempty" example:"559874816"`
	Notes             string `json:"notes,omitempty" example:"native tool format"`
}

func (d ModelDescriptor) BackendType() string {
	if d.Backend == "" {
		return "llama"
	}
	return d.Backend
}

func (d ModelDescriptor) Label() string {
	if d.DisplayLabel != "" {
		return d.DisplayLabel
	}
	return d.Name
}

func (d ModelDescriptor) RecommendedVRAMLabel() string {
	if d.RecommendedVRAMGB <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dGB", d.RecommendedVRAMGB)
}

const (
	residentHeadroomNumerator   = 5
	residentHeadroomDenominator = 4
)

func (d ModelDescriptor) EstimatedResidentBytes() int64 {
	if d.SizeBytes <= 0 {
		return 0
	}
	return (d.SizeBytes + d.MMProjSizeBytes) * residentHeadroomNumerator / residentHeadroomDenominator
}

type FamilyGroup struct {
	Family  string
	Entries []ModelDescriptor
}

func GroupByFamily(entries []ModelDescriptor) []FamilyGroup {
	groups := map[string][]ModelDescriptor{}
	for _, e := range entries {
		key := e.Family
		if key == "" {
			key = e.Name
		}
		groups[key] = append(groups[key], e)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]FamilyGroup, 0, len(keys))
	for _, k := range keys {
		es := groups[k]
		sort.Slice(es, func(i, j int) bool { return es[i].SizeBytes < es[j].SizeBytes })
		out = append(out, FamilyGroup{Family: k, Entries: es})
	}
	return out
}

type Registry interface {
	Resolve(ctx context.Context, name string) (*ModelDescriptor, error)
	List(ctx context.Context) ([]ModelDescriptor, error)
}

type registryImpl struct {
	svc modelregistryservice.Service
}

func New(svc modelregistryservice.Service) Registry {
	return &registryImpl{svc: svc}
}

func (r *registryImpl) Resolve(ctx context.Context, name string) (*ModelDescriptor, error) {
	if r.svc == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	entry, err := r.svc.GetByName(ctx, name)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, err
	}
	descriptor := mergeUserEntry(entry)
	return &descriptor, nil
}

func (r *registryImpl) List(ctx context.Context) ([]ModelDescriptor, error) {
	if r.svc == nil {
		return nil, nil
	}
	entries, err := r.svc.List(ctx, nil, 1000)
	if err != nil {
		return nil, err
	}
	out := make([]ModelDescriptor, 0, len(entries))
	for _, entry := range entries {
		out = append(out, mergeUserEntry(entry))
	}
	return out, nil
}

func mergeUserEntry(e *runtimetypes.ModelRegistryEntry) ModelDescriptor {
	if e == nil {
		return ModelDescriptor{}
	}
	return ModelDescriptor{
		ID:        e.ID,
		Name:      e.Name,
		SourceURL: e.SourceURL,
		SizeBytes: e.SizeBytes,
	}
}
