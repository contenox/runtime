package runtimestate

import (
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func ConnectionSelector(id string) string {
	return "connection:" + strings.TrimSpace(id)
}

func MatchesBackend(selector string, backend runtimetypes.Backend) bool {
	selector = strings.TrimSpace(selector)
	if IsAnyProvider(selector) {
		return true
	}
	if strings.HasPrefix(selector, "connection:") {
		return strings.TrimPrefix(selector, "connection:") == backend.ID
	}
	key := modelrepo.CanonicalBackendType(selector)
	return key != "" && (key == modelrepo.CanonicalBackendType(backend.Type) || key == modelrepo.CanonicalBackendType(backend.Name))
}
