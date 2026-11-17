package modelregistry

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelcontract"
)

// CompletionInstruct is code insertion through an instruction-following chat chain, not native FIM decoding.
const CompletionInstruct = "instruct"

// CompletionPrompt is code insertion through one bounded instruction-prompt request.
const CompletionPrompt = "prompt"

// CompletionFIM is native fill-in-the-middle decoding without a chat chain.
const CompletionFIM = "fim"

// CompletionProfile describes the execution mode, required context, and evidence for code completion.
type CompletionProfile struct {
	FIMFormat     string `json:"fimFormat,omitempty"`
	Mode          string `json:"mode"`
	ContextTokens int    `json:"contextTokens"`
	Evidence      string `json:"evidence"`
}

// NextEditProfile describes a model's independent edit-prediction operation.
type NextEditProfile struct {
	Format        string `json:"format"`
	ContextTokens int    `json:"contextTokens"`
	Evidence      string `json:"evidence"`
}

// SupportsNextEdit reports an explicit supported edit contract with curation evidence.
func (d ModelDescriptor) SupportsNextEdit() bool {
	profile, err := modelcontract.ResolveNextEdit(d.NextEdit.Format)
	return err == nil && d.NextEdit.ContextTokens > 0 && d.NextEdit.ContextTokens <= profile.ContextTokens && strings.TrimSpace(d.NextEdit.Evidence) != ""
}

// EmbeddingProfile describes a dedicated retrieval model's execution and input contract.
type EmbeddingProfile struct {
	Pooling        string `json:"pooling"`
	ContextTokens  int    `json:"contextTokens"`
	Dimension      int    `json:"dimension"`
	Normalize      bool   `json:"normalize"`
	QueryPrefix    string `json:"queryPrefix"`
	DocumentPrefix string `json:"documentPrefix"`
	Evidence       string `json:"evidence"`
}

// SupportsCompletion reports an explicit native FIM, chat-chain, or prompt completion contract.
func (d ModelDescriptor) SupportsCompletion() bool {
	if d.Completion.ContextTokens <= 0 || strings.TrimSpace(d.Completion.Evidence) == "" {
		return false
	}
	if d.Completion.Mode == CompletionFIM {
		_, err := modelcontract.ResolveFIM(d.Completion.FIMFormat)
		return err == nil
	}
	return (d.Completion.Mode == CompletionInstruct || d.Completion.Mode == CompletionPrompt) && d.Completion.FIMFormat == ""
}

// SupportsRetrieval reports an explicit, complete dedicated embedding contract.
func (d ModelDescriptor) SupportsRetrieval() bool {
	return (d.Embedding.Pooling == "last" || d.Embedding.Pooling == "mean" || d.Embedding.Pooling == "cls") &&
		d.Embedding.ContextTokens > 0 && d.Embedding.Dimension > 0 && d.Embedding.Normalize && strings.TrimSpace(d.Embedding.Evidence) != ""
}

// RoleMemoryBytes estimates resident weights, runtime allowance and the role's required KV context.
func (d ModelDescriptor) RoleMemoryBytes(role, kvType string) int64 {
	var tokens int
	switch role {
	case "autocomplete":
		if !d.SupportsCompletion() {
			return 0
		}
		tokens = d.Completion.ContextTokens
	case "embedding":
		if !d.SupportsRetrieval() {
			return 0
		}
		tokens = d.Embedding.ContextTokens
	case "next-edit":
		if !d.SupportsNextEdit() {
			return 0
		}
		tokens = d.NextEdit.ContextTokens
	default:
		return 0
	}
	if d.AutoContextLimit < tokens || !d.AutoKVProfile.Valid() || d.SizeBytes <= 0 {
		return 0
	}
	return costForHotContext(d, tokens, kvType).total()
}

// ContextMemoryBytes estimates weights, runtime overhead and KV for a reviewed context within the qualified native ceiling.
func (d ModelDescriptor) ContextMemoryBytes(tokens int, kvType string) int64 {
	if tokens <= 0 || tokens > d.AutoContextLimit || !d.AutoKVProfile.Valid() || d.SizeBytes <= 0 {
		return 0
	}
	return costForHotContext(d, tokens, kvType).total()
}

// AutoAcquirable reports whether automatic setup may download and assign this
// artifact. Automatic setup acts on the user's behalf, which only a permissive
// license allows.
func (d ModelDescriptor) AutoAcquirable() bool {
	return d.License == "apache-2.0" || d.License == "mit"
}

// SelectRole selects the lowest-resident-cost explicitly qualified role
// candidate. Completion is chosen at the tier where it is good enough rather
// than at the largest tier that fits: a stronger predictor buys nothing the
// user can feel, while the memory it holds is capacity Chat, Next Edit and
// retrieval cannot use on the same device. Names and workflow labels are not
// evidence.
func SelectRole(candidates []ModelDescriptor, role, origin, backend string, budget int64, kvType string) (ModelDescriptor, error) {
	if role != "autocomplete" && role != "next-edit" && role != "embedding" {
		return ModelDescriptor{}, fmt.Errorf("unsupported automatic role %q", role)
	}
	if budget <= 0 {
		return ModelDescriptor{}, errors.New("role selection requires a positive memory budget")
	}
	var eligible []ModelDescriptor
	for _, candidate := range candidates {
		if !candidate.Curated || !candidate.AutoEligible || candidate.BackendType() != backend || origin != "" && candidate.Origin != origin {
			continue
		}
		if !candidate.AutoAcquirable() {
			continue
		}
		cost := candidate.RoleMemoryBytes(role, kvType)
		if cost > 0 && cost <= budget {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return ModelDescriptor{}, fmt.Errorf("%w: %s has no explicit execution contract fitting origin=%s backend=%s memory=%s; "+remedy.AssignRole, ErrNoAutoModel, role, origin, backend, humanGiB(budget))
	}
	sort.Slice(eligible, func(i, j int) bool {
		if role == "autocomplete" && (eligible[i].Completion.Mode == CompletionFIM) != (eligible[j].Completion.Mode == CompletionFIM) {
			return eligible[i].Completion.Mode == CompletionFIM
		}
		a, b := eligible[i].RoleMemoryBytes(role, kvType), eligible[j].RoleMemoryBytes(role, kvType)
		if a != b {
			return a < b
		}
		return eligible[i].Name < eligible[j].Name
	})
	return eligible[0], nil
}
