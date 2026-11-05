package alibabacloud

import (
	"context"
	"net/http"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/openai"
)

const backendType = "alibaba-cloud"

type provider struct {
	modelrepo.Provider
}

func newProvider(apiKey, model, baseURL string, caps modelrepo.CapabilityConfig, client *http.Client, tracker libtracker.ActivityTracker) *provider {
	if client == nil {
		client = modelrepo.SharedHTTPClient
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	caps.CanAutocomplete = false
	caps.CanNextEdit = false
	caps.Dialect = dialectName
	return &provider{Provider: openai.NewOpenAIProvider(apiKey, model, []string{baseURL}, caps, client, tracker)}
}

func (p *provider) GetType() string       { return backendType }
func (p *provider) CanAutocomplete() bool { return false }
func (p *provider) CanNextEdit() bool     { return false }

func (p *provider) GetAutocompleteConnection(context.Context, string) (modelrepo.LLMAutocompleteClient, error) {
	return nil, modelrepo.ErrAutocompleteUnsupported
}

func (p *provider) GetNextEditConnection(context.Context, string) (modelrepo.LLMNextEditClient, error) {
	return nil, modelrepo.ErrNextEditUnsupported
}

var _ modelrepo.Provider = (*provider)(nil)
