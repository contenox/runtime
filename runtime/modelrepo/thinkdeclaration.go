package modelrepo

import (
	"strings"
	"sync"
)

var (
	thinkDeclarationMu       sync.RWMutex
	thinkDeclarationRefusals = map[string]string{}
)

func RegisterThinkDeclarationRefusal(backendType, reason string) {
	normalized := CanonicalBackendType(backendType)
	if normalized == "" || strings.TrimSpace(reason) == "" {
		panic("modelrepo: a think declaration refusal needs a backend type and a reason")
	}
	thinkDeclarationMu.Lock()
	defer thinkDeclarationMu.Unlock()
	if _, exists := thinkDeclarationRefusals[normalized]; exists {
		panic("modelrepo: think declaration refusal already registered for type " + normalized)
	}
	thinkDeclarationRefusals[normalized] = reason
}

func ThinkDeclarationRefusal(backendType string) (reason string, ok bool) {
	thinkDeclarationMu.RLock()
	defer thinkDeclarationMu.RUnlock()
	reason, ok = thinkDeclarationRefusals[CanonicalBackendType(backendType)]
	return reason, ok
}
