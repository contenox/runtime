package ollama

import "github.com/contenox/runtime/runtime/modelrepo"

func sessionOf(config *modelrepo.ChatConfig, declared bool) string {
	if !declared || config == nil || config.CacheHints == nil {
		return ""
	}
	return config.CacheHints.SessionKey
}
