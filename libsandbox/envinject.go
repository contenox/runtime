package libsandbox

import (
	"strings"
)

func OverlayEnv(env []string, set map[string]string) []string {
	if len(set) == 0 {
		return env
	}
	merged := make(envSet, len(env)+len(set))
	for _, kv := range env {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			merged.set(kv[:eq], kv[eq+1:])
		}
	}
	for k, v := range set {
		merged.set(k, v)
	}
	return merged.list()
}
