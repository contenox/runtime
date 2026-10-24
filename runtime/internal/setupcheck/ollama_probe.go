package setupcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const DefaultOllamaSuggestModel = "qwen2.5:7b"

func resolveOllamaProbeBaseURL() string {
	h := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if h == "" {
		return "http://127.0.0.1:11434"
	}
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		return strings.TrimRight(h, "/")
	}
	return "http://" + strings.TrimLeft(h, "/")
}

func ProbeLocalOllamaAPI(ctx context.Context) (baseURL string, ok bool) {
	baseURL = resolveOllamaProbeBaseURL()
	if _, err := url.Parse(baseURL); err != nil {
		return baseURL, false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return baseURL, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return baseURL, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return baseURL, resp.StatusCode == http.StatusOK
}

func needsNonDefaultOllamaBackendURL(probedBase string) bool {
	u, err := url.Parse(probedBase)
	if err != nil {
		return true
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	host := strings.ToLower(u.Hostname())
	loop := host == "localhost" || host == "127.0.0.1" || host == "::1"
	return !(loop && port == "11434")
}

func ollamaRegistrationCommand(probedBase string) string {
	if needsNonDefaultOllamaBackendURL(probedBase) {
		return fmt.Sprintf("contenox backend add local --type ollama --url %q", probedBase)
	}
	return "contenox backend add local --type ollama"
}

func ollamaBackendAlreadyReachable(r Result) bool {
	for _, c := range r.BackendChecks {
		if strings.EqualFold(strings.TrimSpace(c.Type), "ollama") && c.Reachable {
			return true
		}
	}
	return false
}

func enrichResultWithOllamaAt(r Result, probedBase string) Result {
	if probedBase == "" || ollamaBackendAlreadyReachable(r) {
		return r
	}
	suffix := fmt.Sprintf(" Local Ollama responded at %s — pull a chat model if needed, then register it.", probedBase)
	cmd := ollamaRegistrationCommand(probedBase)
	pull := fmt.Sprintf("ollama pull %s", DefaultOllamaSuggestModel)
	config := fmt.Sprintf("contenox config set default-provider ollama && contenox config set default-model %s", DefaultOllamaSuggestModel)
	full := pull + " && " + cmd + " && " + config

	for i := range r.Issues {
		switch r.Issues[i].Code {
		case "no_backends":
			if strings.Contains(r.Issues[i].Message, "Local Ollama responded at") {
				continue
			}
			r.Issues[i].Message = strings.TrimSpace(r.Issues[i].Message + suffix)
			r.Issues[i].CLICommand = full
		case "default_provider_backend_missing":
			if !strings.EqualFold(strings.TrimSpace(r.DefaultProvider), "ollama") {
				continue
			}
			if strings.Contains(r.Issues[i].Message, "Local Ollama responded at") {
				continue
			}
			r.Issues[i].Message = strings.TrimSpace(r.Issues[i].Message + suffix)
			r.Issues[i].CLICommand = full
		}
	}
	return r
}

func EnrichResultWithOllamaProbe(ctx context.Context, r Result) Result {
	base, ok := ProbeLocalOllamaAPI(ctx)
	if !ok {
		return r
	}
	return enrichResultWithOllamaAt(r, base)
}
