package agentregistryservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

type Service interface {
	Create(ctx context.Context, agent *runtimetypes.Agent) error
	Get(ctx context.Context, id string) (*runtimetypes.Agent, error)
	GetByName(ctx context.Context, name string) (*runtimetypes.Agent, error)
	Update(ctx context.Context, agent *runtimetypes.Agent) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*runtimetypes.Agent, error)
}

type service struct {
	db libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{db: db}
}

func (s *service) store() runtimetypes.Store {
	return runtimetypes.New(s.db.WithoutTransaction())
}

func (s *service) Create(ctx context.Context, agent *runtimetypes.Agent) error {
	if err := validate(agent); err != nil {
		return err
	}
	if agent.ID == "" {
		agent.ID = uuid.NewString()
	}
	if err := s.checkNameAvailable(ctx, agent.Name, agent.ID); err != nil {
		return err
	}
	return s.store().CreateAgent(ctx, agent)
}

func (s *service) Get(ctx context.Context, id string) (*runtimetypes.Agent, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	return s.store().GetAgent(ctx, id)
}

func (s *service) GetByName(ctx context.Context, name string) (*runtimetypes.Agent, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return s.store().GetAgentByName(ctx, name)
}

func (s *service) Update(ctx context.Context, agent *runtimetypes.Agent) error {
	if agent.ID == "" {
		return fmt.Errorf("id is required for update")
	}
	if err := validate(agent); err != nil {
		return err
	}
	if err := s.checkNameAvailable(ctx, agent.Name, agent.ID); err != nil {
		return err
	}
	return s.store().UpdateAgent(ctx, agent)
}

func (s *service) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return s.store().DeleteAgent(ctx, id)
}

func (s *service) List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*runtimetypes.Agent, error) {
	return s.store().ListAgents(ctx, createdAtCursor, limit)
}

var ErrAgentDisabled = errors.New("agentregistryservice: agent is disabled")

type disabledAgentError struct{ name string }

func (e *disabledAgentError) Error() string {
	return fmt.Sprintf("agent %q is disabled; enable it with 'contenox agent enable %q'", e.name, e.name)
}

func (e *disabledAgentError) Unwrap() error { return ErrAgentDisabled }

func ResolveForSpawn(ctx context.Context, svc Service, agentName string) (*runtimetypes.Agent, error) {
	agent, err := svc.GetByName(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("resolve agent %q: %w", agentName, err)
	}
	if !agent.Enabled {
		return nil, &disabledAgentError{name: agentName}
	}
	return agent, nil
}

func (s *service) checkNameAvailable(ctx context.Context, name, excludeID string) error {
	existing, err := s.store().GetAgentByName(ctx, name)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return err
	}
	if existing.ID == excludeID {
		return nil
	}
	return fmt.Errorf("agent: name %q already exists: %w", name, libdb.ErrUniqueViolation)
}

func validate(agent *runtimetypes.Agent) error {
	if agent.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch agent.Kind {
	case runtimetypes.AgentKindExternalACP:
		cfg, err := agent.ExternalACPConfig()
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
	case runtimetypes.AgentKindChain:
		cfg, err := agent.ChainConfig()
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
	case "":
		return fmt.Errorf("kind is required")
	default:
		return fmt.Errorf("unknown agent kind %q: must be %q or %q",
			agent.Kind, runtimetypes.AgentKindExternalACP, runtimetypes.AgentKindChain)
	}
	return nil
}
