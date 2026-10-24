package modelrepo

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
)

type BackendSpec struct {
	Type    string
	BaseURL string
	APIKey  string
}

type ObservedModel struct {
	Name          string
	ContextLength int
	ModifiedAt    time.Time
	Size          int64
	Digest        string
	CapabilityConfig
	Meta map[string]string
}

type CatalogProvider interface {
	Type() string
	ListModels(ctx context.Context) ([]ObservedModel, error)
	ProviderFor(model ObservedModel) Provider
}

type CatalogFactory interface {
	NewCatalogProvider(spec BackendSpec, opts ...CatalogOption) (CatalogProvider, error)
}

type CatalogOptions struct {
	Authorize  func(context.Context) (http.Header, error)
	HTTPClient *http.Client
	Tracker    libtracker.ActivityTracker
}

type CatalogOption func(*CatalogOptions)

func WithCatalogAuthorizer(authorize func(context.Context) (http.Header, error)) CatalogOption {
	return func(opts *CatalogOptions) { opts.Authorize = authorize }
}

func WithCatalogHTTPClient(client *http.Client) CatalogOption {
	return func(opts *CatalogOptions) {
		opts.HTTPClient = client
	}
}

func WithCatalogTracker(tracker libtracker.ActivityTracker) CatalogOption {
	return func(opts *CatalogOptions) {
		opts.Tracker = tracker
	}
}

type CatalogProviderConstructor func(spec BackendSpec, opts CatalogOptions) (CatalogProvider, error)

type registryCatalogFactory struct{}

var (
	catalogRegistryMu sync.RWMutex
	catalogRegistry   = map[string]CatalogProviderConstructor{}
)

func HasCatalogProvider(backendType string) bool {
	catalogRegistryMu.RLock()
	defer catalogRegistryMu.RUnlock()
	_, ok := catalogRegistry[strings.ToLower(strings.TrimSpace(backendType))]
	return ok
}

func RegisterCatalogProvider(backendType string, constructor CatalogProviderConstructor) {
	normalized := strings.ToLower(strings.TrimSpace(backendType))
	if normalized == "" {
		panic("modelrepo: catalog provider type cannot be empty")
	}
	if constructor == nil {
		panic("modelrepo: catalog provider constructor cannot be nil")
	}

	catalogRegistryMu.Lock()
	defer catalogRegistryMu.Unlock()
	if _, exists := catalogRegistry[normalized]; exists {
		panic("modelrepo: catalog provider already registered for type " + normalized)
	}
	catalogRegistry[normalized] = constructor
}

func DefaultCatalogFactory() CatalogFactory {
	return registryCatalogFactory{}
}

func NewCatalogProvider(spec BackendSpec, opts ...CatalogOption) (CatalogProvider, error) {
	return DefaultCatalogFactory().NewCatalogProvider(spec, opts...)
}

func (registryCatalogFactory) NewCatalogProvider(spec BackendSpec, opts ...CatalogOption) (CatalogProvider, error) {
	normalized := CanonicalBackendType(spec.Type)
	if normalized == "" {
		return nil, fmt.Errorf("catalog provider type is required")
	}

	options := CatalogOptions{
		HTTPClient: SharedHTTPClient,
		Tracker:    libtracker.NoopTracker{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.HTTPClient == nil {
		options.HTTPClient = SharedHTTPClient
	}
	if options.Tracker == nil {
		options.Tracker = libtracker.NoopTracker{}
	}

	catalogRegistryMu.RLock()
	constructor, ok := catalogRegistry[normalized]
	catalogRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported catalog provider type %q", spec.Type)
	}

	spec.Type = normalized
	return constructor(spec, options)
}
