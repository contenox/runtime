package tokenizerservice

import (
	"net/http"
	"sync"

	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/libs/libollama"
)

type Service struct {
	mu         sync.RWMutex
	tokenizer  libollama.Tokenizer
	config     Config
	httpClient *http.Client
}

type Config struct {
	Models         map[string]string
	FallbackModel  string
	AuthToken      string
	PreloadModels  []string
	UseDefaultURLs bool
}

func New(initial Config) (*Service, error) {
	svc := &Service{
		config: Config{
			Models:         make(map[string]string),
			UseDefaultURLs: true,
		},
		httpClient: http.DefaultClient,
	}

	// Apply initial configuration
	if initial.Models != nil {
		svc.config.Models = initial.Models
	}
	if initial.FallbackModel != "" {
		svc.config.FallbackModel = initial.FallbackModel
	}
	if initial.AuthToken != "" {
		svc.config.AuthToken = initial.AuthToken
	}
	if initial.PreloadModels != nil {
		svc.config.PreloadModels = initial.PreloadModels
	}
	svc.config.UseDefaultURLs = initial.UseDefaultURLs
	err := svc.applyConfig()
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// Core configuration management
func (s *Service) applyConfig() error {
	opts := []libollama.TokenizerOption{}

	// Model configuration
	if s.config.UseDefaultURLs {
		opts = append(opts, libollama.TokenizerWithCustomModels(s.config.Models))
	} else {
		opts = append(opts, libollama.TokenizerWithModelMap(s.config.Models))
	}

	// Authentication
	if s.config.AuthToken != "" {
		opts = append(opts, libollama.TokenizerWithToken(s.config.AuthToken))
	}

	// Network configuration
	opts = append(opts, libollama.TokenizerWithHTTPClient(s.httpClient))

	// Model defaults
	if s.config.FallbackModel != "" {
		opts = append(opts, libollama.TokenizerWithFallbackModel(s.config.FallbackModel))
	}

	// Preloading
	if len(s.config.PreloadModels) > 0 {
		opts = append(opts, libollama.TokenizerWithPreloadedModels(s.config.PreloadModels...))
	}

	// Create new tokenizer instance
	newTokenizer, err := libollama.NewTokenizer(opts...)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenizer = newTokenizer
	return nil
}

// Model management
func (s *Service) AddModel(name, modelURL string) error {
	s.mu.Lock()
	s.config.Models[name] = modelURL
	s.mu.Unlock()
	return s.applyConfig()
}

func (s *Service) RemoveModel(name string) error {
	s.mu.Lock()
	delete(s.config.Models, name)
	s.mu.Unlock()
	return s.applyConfig()
}

func (s *Service) ReplaceAllModels(models map[string]string) error {
	s.mu.Lock()
	s.config.Models = models
	s.config.UseDefaultURLs = false
	s.mu.Unlock()
	return s.applyConfig()
}

// Security configuration
func (s *Service) SetAuthToken(token string) error {
	s.mu.Lock()
	s.config.AuthToken = token
	s.mu.Unlock()
	return s.applyConfig()
}

// Network configuration
func (s *Service) SetHTTPClient(client *http.Client) error {
	s.mu.Lock()
	s.httpClient = client
	s.mu.Unlock()
	return s.applyConfig()
}

// Fallback model management
func (s *Service) SetFallbackModel(name string) error {
	s.mu.Lock()
	s.config.FallbackModel = name
	s.mu.Unlock()
	return s.applyConfig()
}

// Preloading configuration
func (s *Service) SetPreloadModels(models []string) error {
	s.mu.Lock()
	s.config.PreloadModels = models
	s.mu.Unlock()
	return s.applyConfig()
}

// Existing service methods with thread safety
func (s *Service) Tokenize(modelName string, prompt string) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenizer.Tokenize(modelName, prompt)
}

func (s *Service) CountTokens(modelName string, prompt string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenizer.CountTokens(modelName, prompt)
}

func (s *Service) AvailableModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenizer.AvailableModels()
}

func (s *Service) OptimalModel(baseModel string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenizer.OptimalTokenizerModel(baseModel)
}

func (s *Service) GetServiceName() string  { return "tokenizerservice" }
func (s *Service) GetServiceGroup() string { return serverops.DefaultDefaultServiceGroup }
