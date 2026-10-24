package vertex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type vertexProvider struct {
	id              string
	publisher       string
	modelName       string
	baseURL         string
	credJSON        string
	httpClient      *http.Client
	contextLength   int
	maxOutputTokens int
	canChat         bool
	canPrompt       bool
	canEmbed        bool
	canStream       bool
	canThink        bool
	canVision       bool
	canAudio        bool
	tracker         libtracker.ActivityTracker
	expressKey      string

	tokenOnce    sync.Once
	tokenSrc     oauth2.TokenSource
	tokenErr     error
	authOnce     sync.Once
	quotaProject string
	authErr      error
}

func NewVertexProvider(publisher, modelName string, baseURLs []string, cap modelrepo.CapabilityConfig, credJSON string, httpClient *http.Client, tracker libtracker.ActivityTracker) modelrepo.Provider {
	if httpClient == nil {
		httpClient = modelrepo.SharedHTTPClient
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	baseURL := ""
	if len(baseURLs) > 0 {
		baseURL = baseURLs[0]
	}
	return &vertexProvider{
		id:              fmt.Sprintf("vertex-%s:%s", publisher, modelName),
		publisher:       publisher,
		modelName:       modelName,
		baseURL:         baseURL,
		credJSON:        credJSON,
		httpClient:      httpClient,
		contextLength:   cap.ContextLength,
		maxOutputTokens: cap.MaxOutputTokens,
		canChat:         cap.CanChat,
		canPrompt:       cap.CanPrompt,
		canEmbed:        cap.CanEmbed,
		canStream:       cap.CanStream,
		canThink:        cap.CanThink,
		canVision:       cap.CanVision,
		canAudio:        cap.CanAudio,
		tracker:         tracker,
	}
}

func (p *vertexProvider) GetBackendIDs() []string { return []string{p.baseURL} }
func (p *vertexProvider) ModelName() string       { return p.modelName }
func (p *vertexProvider) GetID() string           { return p.id }
func (p *vertexProvider) GetType() string         { return "vertex-" + p.publisher }
func (p *vertexProvider) GetContextLength() int   { return p.contextLength }
func (p *vertexProvider) GetMaxOutputTokens() int { return p.maxOutputTokens }
func (p *vertexProvider) CanChat() bool           { return p.canChat }
func (p *vertexProvider) CanEmbed() bool          { return p.canEmbed }
func (p *vertexProvider) CanStream() bool         { return p.canStream }
func (p *vertexProvider) CanPrompt() bool         { return p.canPrompt }
func (p *vertexProvider) CanThink() bool          { return p.canThink }
func (p *vertexProvider) CanVision() bool         { return p.canVision }
func (p *vertexProvider) CanAudio() bool          { return p.canAudio }

func (p *vertexProvider) tokenFn(_ context.Context) (string, error) {
	p.tokenOnce.Do(func() {
		p.tokenSrc, p.tokenErr = NewTokenSource(context.Background(), p.credJSON)
	})
	if p.tokenErr != nil {
		return "", p.tokenErr
	}
	tok, err := p.tokenSrc.Token()
	if err != nil {
		return "", fmt.Errorf("vertex AI token: %w", err)
	}
	return tok.AccessToken, nil
}

func (p *vertexProvider) resolveAuth() error {
	p.authOnce.Do(func() {
		if IsExpressAPIKey(p.credJSON) {
			p.expressKey = strings.TrimSpace(p.credJSON)
			return
		}
		project, err := QuotaProjectFor(context.Background(), p.credJSON, p.baseURL)
		if err != nil {
			p.authErr = err
			return
		}
		p.quotaProject = project
	})
	return p.authErr
}

func (p *vertexProvider) client() (vertexClient, error) {
	if err := p.resolveAuth(); err != nil {
		return vertexClient{}, err
	}
	return vertexClient{
		baseURL:         p.baseURL,
		publisher:       p.publisher,
		modelName:       p.modelName,
		contextLength:   p.contextLength,
		maxOutputTokens: p.maxOutputTokens,
		credJSON:        p.credJSON,
		apiKey:          p.expressKey,
		quotaProject:    p.quotaProject,
		httpClient:      p.httpClient,
		canThink:        p.canThink,
		tracker:         p.tracker,
		tokenFn:         p.tokenFn,
	}, nil
}

func (p *vertexProvider) GetChatConnection(_ context.Context, _ string) (modelrepo.LLMChatClient, error) {
	if !p.CanChat() {
		return nil, fmt.Errorf("model %s does not support chat interactions", p.modelName)
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return &vertexChatClient{vertexClient: c}, nil
}

func (p *vertexProvider) GetPromptConnection(_ context.Context, _ string) (modelrepo.LLMPromptExecClient, error) {
	if !p.CanPrompt() {
		return nil, fmt.Errorf("model %s does not support prompt interactions", p.modelName)
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return &vertexPromptClient{vertexClient: c}, nil
}

func (p *vertexProvider) GetStreamConnection(_ context.Context, _ string) (modelrepo.LLMStreamClient, error) {
	if !p.CanStream() {
		return nil, fmt.Errorf("model %s does not support streaming interactions", p.modelName)
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return &vertexStreamClient{vertexClient: c}, nil
}

func (p *vertexProvider) GetEmbedConnection(_ context.Context, _ string) (modelrepo.LLMEmbedClient, error) {
	if !p.CanEmbed() {
		return nil, fmt.Errorf("model %s (vertex-%s) does not support embeddings; use a text-embedding model such as gemini-embedding-001", p.modelName, p.publisher)
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return &vertexEmbedClient{vertexClient: c}, nil
}

var _ modelrepo.Provider = (*vertexProvider)(nil)

func (p *vertexProvider) CanAutocomplete() bool { return false }

func (p *vertexProvider) GetAutocompleteConnection(context.Context, string) (modelrepo.LLMAutocompleteClient, error) {
	return nil, modelrepo.ErrAutocompleteUnsupported
}
