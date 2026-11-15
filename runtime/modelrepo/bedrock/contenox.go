package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/smithy-go/auth"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/contenox/runtime/runtime/internal/models/contenoxauth"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type hostedCatalog struct{ opts modelrepo.CatalogOptions }

func init() {
	modelrepo.RegisterCatalogProvider(contenoxauth.ProviderType, func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		if spec.APIKey != "" || (spec.BaseURL != "" && spec.BaseURL != contenoxauth.BaseURL) {
			return nil, errors.New("Contenox uses account sign-in and a fixed endpoint")
		}
		if opts.Authorize == nil {
			return nil, contenoxauth.ErrLoginRequired
		}
		return &hostedCatalog{opts}, nil
	})
}
func (c *hostedCatalog) Type() string { return contenoxauth.ProviderType }

type accountAuth struct{}

func (accountAuth) ResolveAuthSchemes(context.Context, *bedrockruntime.AuthResolverParameters) ([]*auth.Option, error) {
	return []*auth.Option{{SchemeID: auth.SchemeIDAnonymous}}, nil
}

type accountClient struct{ opts modelrepo.CatalogOptions }

func (c accountClient) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != contenoxauth.Origin() {
		return nil, errors.New("unexpected Contenox account endpoint")
	}
	headers, err := c.opts.Authorize(r.Context())
	if err != nil {
		return nil, err
	}
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", headers.Get("Authorization"))
	client := *c.opts.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(req)
}
func (c *hostedCatalog) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", contenoxauth.Origin()+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (accountClient{c.opts}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, contenoxauth.ErrLoginRequired
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Contenox model discovery returned HTTP %d", resp.StatusCode)
	}
	var page struct {
		Data []struct {
			ID      string
			Context int    `json:"context_length"`
			Output  int    `json:"max_output_tokens"`
			Name    string `json:"display_name"`
		}
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&page); err != nil {
		return nil, err
	}
	var list []modelrepo.ObservedModel
	for _, m := range page.Data {
		if m.ID == "" || m.Context <= 0 || m.Output <= 0 {
			return nil, errors.New("invalid hosted model limits")
		}
		list = append(list, modelrepo.ObservedModel{Name: m.ID, ContextLength: m.Context, CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanPrompt: true, CanStream: true, ContextLength: m.Context, MaxOutputTokens: m.Output}, Meta: map[string]string{modelrepo.ObservedDisplayNameMetaKey: m.Name}})
	}
	return list, nil
}
func (c *hostedCatalog) ProviderFor(m modelrepo.ObservedModel) modelrepo.Provider {
	p := NewBedrockProvider("eu-central-1", "", m.Name, m.CapabilityConfig, c.opts.HTTPClient, c.opts.Tracker).(*bedrockProvider)
	p.id = "contenox-" + m.Name
	p.hosted = true
	p.once.Do(func() {
		p.api = bedrockruntime.New(bedrockruntime.Options{Region: p.region, AuthSchemeResolver: accountAuth{}, BaseEndpoint: aws.String(contenoxauth.Origin()), HTTPClient: accountClient{c.opts}, RetryMaxAttempts: 1})
	})
	return p
}
