package inferencesetup

import (
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelregistry"
)

// RolesServed lists the inference roles this entry's curated contracts qualify
// it for: chat and recovery need a tool-capable chat tier, the remaining roles
// need their explicit execution contract. The list is never nil.
func RolesServed(d modelregistry.ModelDescriptor) []string {
	if role := transport.MediaRoleForPipeline(d.Pipeline); role != "" {
		return []string{role}
	}
	roles := make([]string, 0, 5)
	if d.AgenticTier == modelregistry.AgenticTierAgentic || d.AgenticTier == modelregistry.AgenticTierToolChat {
		roles = append(roles, RoleChat, RoleRecovery)
	}
	if d.SupportsCompletion() {
		roles = append(roles, RoleAutocomplete)
	}
	if d.SupportsNextEdit() {
		roles = append(roles, RoleNextEdit)
	}
	if d.SupportsRetrieval() {
		roles = append(roles, RoleEmbedding)
	}
	return roles
}
