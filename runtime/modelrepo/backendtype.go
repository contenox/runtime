package modelrepo

import "strings"

const ScriptedTestBackendType = "scripted-test"

func CanonicalBackendType(backendType string) string {
	normalized := strings.ToLower(strings.TrimSpace(backendType))
	if normalized == "local" {
		return "llama"
	}
	return normalized
}
