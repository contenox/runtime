package externalagents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type Deps struct {
	Agents      agentregistryservice.Service
	DB          libdb.DBManager
	ContenoxDir string
	Home        string
	Tracker     libtracker.ActivityTracker
	LookPath    func(file string) (string, error)
}

type Service struct {
	deps Deps

	mu       sync.Mutex
	installs map[string]*installJob
	logins   map[string]*loginJob
}

func New(deps Deps) *Service {
	if deps.Tracker == nil {
		deps.Tracker = libtracker.NoopTracker{}
	}
	return &Service{deps: deps, installs: map[string]*installJob{}, logins: map[string]*loginJob{}}
}

type Agent struct {
	ID         string                         `json:"id"`
	Name       string                         `json:"name"`
	Title      string                         `json:"title"`
	Enabled    bool                           `json:"enabled"`
	Preset     string                         `json:"preset,omitempty"`
	Version    string                         `json:"version,omitempty"`
	Command    string                         `json:"command"`
	Args       []string                       `json:"args"`
	Env        map[string]string              `json:"env"`
	Paths      []runtimetypes.ExternalACPPath `json:"paths"`
	Modes      []string                       `json:"modes"`
	McpServers []string                       `json:"mcpServers"`
	Sandbox    Sandbox                        `json:"sandbox"`
	CreatedAt  time.Time                      `json:"createdAt"`
	UpdatedAt  time.Time                      `json:"updatedAt"`
}

type Sandbox struct {
	ReadsLimited bool `json:"readsLimited"`
}

func sandboxFacts() Sandbox {
	return Sandbox{ReadsLimited: goruntime.GOOS == "linux"}
}

type Input struct {
	Name       string                         `json:"name"`
	Enabled    *bool                          `json:"enabled,omitempty"`
	Command    string                         `json:"command"`
	Args       []string                       `json:"args"`
	Env        map[string]string              `json:"env"`
	Paths      []runtimetypes.ExternalACPPath `json:"paths"`
	Modes      []string                       `json:"modes"`
	McpServers []string                       `json:"mcpServers"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func viewOf(a *runtimetypes.Agent) (Agent, error) {
	cfg, err := a.ExternalACPConfig()
	if err != nil {
		return Agent{}, err
	}
	v := Agent{
		ID:         a.ID,
		Name:       a.Name,
		Title:      a.Name,
		Enabled:    a.Enabled,
		Command:    cfg.Command,
		Args:       nonNilStrings(cfg.Args),
		Env:        cfg.Env,
		Paths:      cfg.Paths,
		Modes:      nonNilStrings(cfg.Modes),
		McpServers: nonNilStrings(cfg.McpServers),
		Sandbox:    sandboxFacts(),
		CreatedAt:  a.CreatedAt,
		UpdatedAt:  a.UpdatedAt,
	}
	if v.Env == nil {
		v.Env = map[string]string{}
	}
	if v.Paths == nil {
		v.Paths = []runtimetypes.ExternalACPPath{}
	}
	if a.Source != nil && *a.Source == runtimetypes.AgentSourceRegistry && a.RegistryID != nil {
		v.Preset = *a.RegistryID
		if p, ok := presetByID(v.Preset); ok {
			v.Title = p.Title
		}
		if a.RegistryVersion != nil {
			v.Version = *a.RegistryVersion
		}
	}
	return v, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Service) List(ctx context.Context) ([]Agent, error) {
	var out []Agent
	var cursor *time.Time
	for {
		page, err := s.deps.Agents.List(ctx, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, a := range page {
			if a.Kind != runtimetypes.AgentKindExternalACP {
				continue
			}
			v, err := viewOf(a)
			if err != nil {
				continue
			}
			out = append(out, v)
		}
		if len(page) < 100 {
			break
		}
		last := page[len(page)-1].CreatedAt
		cursor = &last
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if out == nil {
		out = []Agent{}
	}
	return out, nil
}

func (s *Service) get(ctx context.Context, name string) (*runtimetypes.Agent, error) {
	a, err := s.deps.Agents.GetByName(ctx, name)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return nil, fmt.Errorf("external agent %q: %w", name, apiframework.ErrNotFound)
		}
		return nil, err
	}
	if a.Kind != runtimetypes.AgentKindExternalACP {
		return nil, fmt.Errorf("agent %q is not an external agent: %w", name, apiframework.ErrNotFound)
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, name string) (Agent, error) {
	a, err := s.get(ctx, name)
	if err != nil {
		return Agent{}, err
	}
	return viewOf(a)
}

func configFromInput(in Input) runtimetypes.ExternalACPConfig {
	return runtimetypes.ExternalACPConfig{
		Transport:  runtimetypes.ExternalACPTransportStdio,
		Command:    strings.TrimSpace(in.Command),
		Args:       in.Args,
		Env:        in.Env,
		Paths:      in.Paths,
		Modes:      in.Modes,
		McpServers: in.McpServers,
	}
}

func validateInput(in Input) error {
	if !namePattern.MatchString(in.Name) {
		return fmt.Errorf("name %q must be 1-63 lowercase letters, digits or dashes, starting with a letter or digit: %w", in.Name, apiframework.ErrInvalidParameterValue)
	}
	if strings.TrimSpace(in.Command) == "" {
		return fmt.Errorf("command is required: %w", apiframework.ErrMissingParameter)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in Input) (Agent, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validateInput(in); err != nil {
		return Agent{}, err
	}
	if _, ok := presetByAgentName(in.Name); ok {
		return Agent{}, fmt.Errorf("name %q is reserved for its preset; install the preset or pick another name: %w", in.Name, apiframework.ErrConflict)
	}
	cfg := configFromInput(in)
	if err := cfg.Validate(); err != nil {
		return Agent{}, fmt.Errorf("%v: %w", err, apiframework.ErrInvalidParameterValue)
	}
	source := runtimetypes.AgentSourceManual
	a := &runtimetypes.Agent{Name: in.Name, Enabled: in.Enabled == nil || *in.Enabled, Source: &source}
	if err := a.SetExternalACPConfig(cfg); err != nil {
		return Agent{}, err
	}
	if err := s.deps.Agents.Create(ctx, a); err != nil {
		if errors.Is(err, libdb.ErrUniqueViolation) {
			return Agent{}, fmt.Errorf("an agent named %q already exists: %w", in.Name, apiframework.ErrConflict)
		}
		return Agent{}, err
	}
	return viewOf(a)
}

func (s *Service) Update(ctx context.Context, name string, in Input) (Agent, error) {
	a, err := s.get(ctx, name)
	if err != nil {
		return Agent{}, err
	}
	in.Name = name
	if err := validateInput(in); err != nil {
		return Agent{}, err
	}
	cfg := configFromInput(in)
	if err := cfg.Validate(); err != nil {
		return Agent{}, fmt.Errorf("%v: %w", err, apiframework.ErrInvalidParameterValue)
	}
	if err := a.SetExternalACPConfig(cfg); err != nil {
		return Agent{}, err
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if err := s.deps.Agents.Update(ctx, a); err != nil {
		return Agent{}, err
	}
	return viewOf(a)
}

func (s *Service) SetEnabled(ctx context.Context, name string, enabled bool) (Agent, error) {
	a, err := s.get(ctx, name)
	if err != nil {
		return Agent{}, err
	}
	a.Enabled = enabled
	if err := s.deps.Agents.Update(ctx, a); err != nil {
		return Agent{}, err
	}
	return viewOf(a)
}

func (s *Service) Delete(ctx context.Context, name string) error {
	a, err := s.get(ctx, name)
	if err != nil {
		return err
	}
	s.cancelLogin(name)
	return s.deps.Agents.Delete(ctx, a.ID)
}

func presetByAgentName(name string) (Preset, bool) {
	for _, p := range presets {
		if p.AgentName == name {
			return p, true
		}
	}
	return Preset{}, false
}
