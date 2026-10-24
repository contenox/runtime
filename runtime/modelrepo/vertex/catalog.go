package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/contenox/runtime/libtracker"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/gemini"
)

func init() {
	modelrepo.RegisterCatalogProvider("vertex-google", newGoogleCatalog)
}

type googleCatalogProvider struct {
	spec       modelrepo.BackendSpec
	httpClient *http.Client
	tracker    libtracker.ActivityTracker
	tokenFn    func(context.Context) (string, error)
	authOnce   sync.Once
	resolved   vertexAuth
	authErr    error
}

type vertexAuth struct {
	apiKey       string
	quotaProject string
	bearer       func(context.Context) (string, error)
}

func (a vertexAuth) apply(ctx context.Context, req *http.Request) error {
	return ApplyVertexAuth(ctx, req, a.apiKey, a.quotaProject, a.bearer)
}

func (p *googleCatalogProvider) auth(ctx context.Context) (vertexAuth, error) {
	p.authOnce.Do(func() {
		if IsExpressAPIKey(p.spec.APIKey) {
			p.resolved = vertexAuth{apiKey: strings.TrimSpace(p.spec.APIKey)}
			return
		}
		bearer := p.tokenFn
		if bearer == nil {
			bearer = func(ctx context.Context) (string, error) {
				return BearerTokenWithCreds(ctx, p.spec.APIKey)
			}
		}
		project, err := QuotaProjectFor(ctx, p.spec.APIKey, p.spec.BaseURL)
		if err != nil {
			p.authErr = err
			return
		}
		p.resolved = vertexAuth{bearer: bearer, quotaProject: project}
	})
	if p.authErr != nil {
		return vertexAuth{}, p.authErr
	}
	return p.resolved, nil
}

func newGoogleCatalog(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
	if spec.BaseURL == "" {
		return nil, fmt.Errorf("vertex-google backend requires --url: the regional endpoint with project and location (https://us-central1-aiplatform.googleapis.com/v1/projects/MY_PROJECT/locations/us-central1), or the express mode global endpoint (https://aiplatform.googleapis.com/v1)")
	}
	client := opts.HTTPClient
	if client == nil {
		client = modelrepo.SharedHTTPClient
	}
	return &googleCatalogProvider{
		spec:       spec,
		httpClient: client,
		tracker:    opts.Tracker,
	}, nil
}

func (p *googleCatalogProvider) Type() string { return "vertex-google" }

func (p *googleCatalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	return p.listGoogleModelsFromVertexPublisher(ctx)
}

func (p *googleCatalogProvider) listGoogleModelsFromVertexPublisher(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	auth, err := p.auth(ctx)
	if err != nil {
		return nil, err
	}
	names, err := listVertexPublisherModelNames(ctx, p.spec.BaseURL, "google", p.httpClient, auth)
	if err != nil {
		return nil, err
	}
	out := make([]modelrepo.ObservedModel, 0, len(names))
	for _, name := range names {
		out = append(out, enrichGooglePublisherModel(name))
	}
	return out, nil
}

func enrichGooglePublisherModel(name string) modelrepo.ObservedModel {
	n := strings.ToLower(name)
	om := modelrepo.ObservedModel{Name: name}
	if fact, documented := gemini.ModelFactsFor(name); documented {
		om.ContextLength = fact.ContextLength
		om.MaxOutputTokens = fact.MaxOutputTokens
	}
	switch {
	case strings.Contains(n, "embed"):
		om.CanEmbed = true
	case strings.Contains(n, "imagen") || strings.Contains(n, "veo-") || strings.Contains(n, "tts") ||
		strings.Contains(n, "lyria") || strings.Contains(n, "nano-banana") || strings.Contains(n, "aqa"):
	default:
		om.CanChat = true
		om.CanPrompt = true
		om.CanStream = true
		om.CanVision = modelrepo.GeminiModelSupportsVision(name)
		om.CanAudio = modelrepo.GeminiModelSupportsAudio(name)
		om.ThinkingTypes = gemini.ThinkingDeclarationFor(name)
		om.CanThink = om.ThinkingTypes != nil
	}
	return om
}

func vertexRegionalPublisherListURL(vertexLocationBaseURL, publisher string) (string, error) {
	base := strings.TrimSpace(vertexLocationBaseURL)
	if base == "" {
		return "", fmt.Errorf("empty backend base URL")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse backend base URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("backend base URL has no host")
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/v1beta1/publishers/%s/models", scheme, u.Host, publisher), nil
}

func listVertexPublisherModelNames(ctx context.Context, vertexLocationBaseURL, publisher string, httpClient *http.Client, auth vertexAuth) ([]string, error) {
	listURLPrefix, err := vertexRegionalPublisherListURL(vertexLocationBaseURL, publisher)
	if err != nil {
		return nil, fmt.Errorf("vertex-%s list models: %w", publisher, err)
	}

	var names []string
	pageToken := ""

	for {
		url := listURLPrefix + "?pageSize=100"
		if pageToken != "" {
			url += "&pageToken=" + pageToken
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if err := auth.apply(ctx, req); err != nil {
			return nil, fmt.Errorf("vertex-%s list models: %w", publisher, err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("vertex publisher list returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var payload struct {
			PublisherModels []struct {
				Name string `json:"name"`
			} `json:"publisherModels"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode vertex publisher model list: %w", err)
		}

		for _, item := range payload.PublisherModels {
			name := item.Name
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			names = append(names, name)
		}

		pageToken = payload.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return names, nil
}

func (p *googleCatalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return NewVertexProvider("google", model.Name, []string{p.spec.BaseURL}, model.CapabilityConfig, p.spec.APIKey, p.httpClient, p.tracker)
}
