package modelrepo_test

import (
	"os"
	"strings"
	"testing"
)

const runVLLMTestsEnv = "CONTENOX_RUN_VLLM_TESTS"

func requireVLLMContainerTests(t *testing.T) {
	t.Helper()
	if envTruthy(os.Getenv(runVLLMTestsEnv)) {
		return
	}
	t.Skipf("skipping vLLM container test; set %s=1 to run", runVLLMTestsEnv)
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

const runOllamaTestsEnv = "CONTENOX_RUN_OLLAMA_TESTS"

// requireOllamaContainerTests gates the Ollama system tests: they start a
// container and pull multi-GB models, so a plain `go test ./...` must not run
// them just because Docker happens to be healthy.
func requireOllamaContainerTests(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Ollama container test in -short mode")
	}
	if envTruthy(os.Getenv(runOllamaTestsEnv)) {
		return
	}
	t.Skipf("skipping Ollama container test (starts a container and pulls multi-GB models); set %s=1 to run", runOllamaTestsEnv)
}
