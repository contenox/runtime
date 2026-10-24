package serverapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/apiframework/middleware"
	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/accessapi"
	"github.com/contenox/runtime/runtime/internal/agentregistryapi"
	"github.com/contenox/runtime/runtime/internal/approvalapi"
	"github.com/contenox/runtime/runtime/internal/backendapi"
	"github.com/contenox/runtime/runtime/internal/compatapi"
	"github.com/contenox/runtime/runtime/internal/fleetapi"
	"github.com/contenox/runtime/runtime/internal/hitlpolicyapi"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/internal/mcpserverapi"
	"github.com/contenox/runtime/runtime/internal/missionapi"
	"github.com/contenox/runtime/runtime/internal/missionchangesapi"
	"github.com/contenox/runtime/runtime/internal/modelregistryapi"
	"github.com/contenox/runtime/runtime/internal/operatorinboxapi"
	"github.com/contenox/runtime/runtime/internal/providerapi"
	"github.com/contenox/runtime/runtime/internal/setupapi"
	"github.com/contenox/runtime/runtime/internal/shellenvapi"
	"github.com/contenox/runtime/runtime/internal/taskchainapi"
	"github.com/contenox/runtime/runtime/internal/taskeventsapi"
	"github.com/contenox/runtime/runtime/internal/taskexecapi"
	"github.com/contenox/runtime/runtime/internal/terminalapi"
	"github.com/contenox/runtime/runtime/internal/toolsapi"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/mcpserverservice"
	"github.com/contenox/runtime/runtime/missionchanges"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelregistryservice"
	"github.com/contenox/runtime/runtime/operatorinbox"
	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/shellenvservice"
	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/contenox/runtime/runtime/taskchainservice"
	"github.com/contenox/runtime/runtime/terminalservice"
	"github.com/contenox/runtime/runtime/toolsproviderservice"
	"github.com/contenox/runtime/runtime/version"
	"github.com/contenox/runtime/runtime/vfs"
)

type Config struct {
	Addr                 string `json:"addr"`
	Port                 string `json:"port"`
	Token                string `json:"token"`
	UIBaseURL            string `json:"ui_base_url"`
	AllowedAPIOrigins    string `json:"allowed_api_origins"`
	ProxyOrigin          string `json:"proxy_origin"`
	HarnessDevProxyURL   string `json:"harness_dev_proxy_url"`
	TerminalEnabled      string `json:"terminal_enabled"`
	TerminalAllowedRoot  string `json:"terminal_allowed_root"`
	TerminalShell        string `json:"terminal_shell"`
	TerminalIdleTimeout  string `json:"terminal_idle_timeout"`
	TerminalMaxSessions  string `json:"terminal_max_sessions"`
	WorkspaceRoots       string `json:"workspace_roots"`
	HITLApprovalTimeout  string `json:"hitl_approval_timeout"`
	TurnMax              string `json:"contenox_turn_max"`
	TurnGrace            string `json:"contenox_turn_grace"`
	SandboxShellScrub    string `json:"sandbox_shell_scrub"`
	SandboxTerminalScrub string `json:"sandbox_terminal_scrub"`
	SandboxEnvAllow      string `json:"sandbox_env_allow"`
	SandboxEnvDeny       string `json:"sandbox_env_deny"`
}

type Dependencies struct {
	DB                    libdb.DBManager
	PubSub                libbus.Messenger
	State                 *runtimestate.State
	ToolsProviderService  toolsproviderservice.Service
	Auth                  middleware.AuthZReader
	Agent                 agentservice.Agent
	Chains                taskchainservice.Service
	Fleet                 fleetservice.Service
	Missions              missionservice.Service
	MissionChanges        missionchanges.Service
	OperatorInbox         operatorinbox.Service
	HITL                  hitlservice.Service
	Tracker               libtracker.ActivityTracker
	WorkspaceID           string
	ProjectRoot           string
	ContenoxDir           string
	WorkspaceRoots        *vfs.Factory
	WorkspaceRootMutators *localfileapi.RootsMutators
	Defaults              stateservice.RuntimeDefaults
	TerminalService       terminalservice.Service
	TerminalEnabled       bool
	HITLPolicySource      hitlservice.PolicySource
	HITLDefaultPolicyName string
}

type emptyKVReader struct{}

func (emptyKVReader) GetKV(context.Context, string, interface{}) error {
	return fmt.Errorf("serverapi: no active policy override")
}

func workspaceHITLFactory(deps Dependencies) localfileapi.PolicyEvaluatorFactory {
	if deps.HITLPolicySource == nil || deps.DB == nil {
		return nil
	}
	src := deps.HITLPolicySource
	defaultPolicy := deps.HITLDefaultPolicyName
	dbm := deps.DB
	return func(policyName string) hitlservice.Service {
		store := runtimetypes.New(dbm.WithoutTransaction())
		if strings.TrimSpace(policyName) == "" {
			return hitlservice.NewWithDefaultPolicy(src, runtimetypes.LocalTenantID, store, libtracker.NoopTracker{}, defaultPolicy)
		}
		return hitlservice.NewWithDefaultPolicy(src, runtimetypes.LocalTenantID, emptyKVReader{}, libtracker.NoopTracker{}, policyName)
	}
}

func New(ctx context.Context, mux *http.ServeMux, nodeInstanceID, tenancy string, config *Config, deps ...Dependencies) (func() error, error) {
	if mux == nil {
		return nil, fmt.Errorf("serverapi: mux is nil")
	}
	if config == nil {
		config = &Config{}
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_ = apiframework.Error(w, r, apiframework.ErrNotFound, apiframework.ListOperation)
	})
	AddHealthRoutes(mux)
	AddVersionRoutes(mux, version.Get(), nodeInstanceID, tenancy)

	if len(deps) > 0 {
		if err := registerProductRoutes(ctx, mux, config, deps[0]); err != nil {
			return nil, err
		}
	}
	return func() error { return nil }, nil
}

func registerProductRoutes(ctx context.Context, mux *http.ServeMux, config *Config, deps Dependencies) error {
	_ = ctx
	if deps.DB == nil || deps.State == nil {
		return nil
	}

	store := runtimetypes.New(deps.DB.WithoutTransaction())
	backendSvc := backendservice.New(deps.DB)
	stateSvc := stateservice.New(deps.State, deps.DB, deps.WorkspaceID)

	agentregistryapi.AddAgentRegistryRoutes(mux, agentregistryservice.New(deps.DB))

	backendapi.AddStateRoutes(mux, stateSvc)
	backendapi.AddModelRoutes(mux, stateSvc, deps.Defaults)
	backendapi.AddBackendRoutes(mux, backendSvc, stateSvc)

	registrySvc := modelregistryservice.New(deps.DB)
	registry := modelregistry.New(registrySvc)
	modelregistryapi.AddRoutes(mux, registrySvc, registry, backendSvc, store)

	setupapi.AddSetupRoutes(mux, stateSvc, deps.Auth)
	shellenvapi.AddShellEnvRoutes(mux, shellenvservice.New(deps.DB), deps.Auth)
	providerSvc := providerservice.New(deps.DB, deps.WorkspaceID)
	providerapi.AddProviderRoutes(mux, providerSvc)

	if deps.WorkspaceRoots != nil {
		if err := localfileapi.AddWorkspaceRoutes(mux, deps.WorkspaceRoots, workspaceHITLFactory(deps)); err != nil {
			return fmt.Errorf("workspace files: %w", err)
		}
		localfileapi.AddWorkspaceRootsRoutes(mux, deps.WorkspaceRoots, deps.WorkspaceRootMutators)
		localfileapi.AddWorkspaceSearchRoutes(mux, deps.WorkspaceRoots)
		localfileapi.AddWorkspaceFindRoutes(mux, deps.WorkspaceRoots, workspaceHITLFactory(deps))
		accessapi.AddRoutes(mux, deps.WorkspaceRoots, workspaceHITLFactory(deps))
	} else if deps.ProjectRoot != "" {
		projectFiles, err := localfileservice.New(deps.ProjectRoot)
		if err != nil {
			return fmt.Errorf("project files: %w", err)
		}
		localfileapi.AddRoutes(mux, projectFiles)
	}
	chains := deps.Chains
	if deps.ContenoxDir != "" {
		chainFiles, err := localfileservice.NewPrivileged(deps.ContenoxDir)
		if err != nil {
			return fmt.Errorf("chain files: %w", err)
		}
		if chains == nil {
			chains = taskchainservice.NewLocal(chainFiles)
		}
		taskchainapi.AddTaskChainRoutes(mux, chains)
		hitlpolicyapi.AddRoutes(mux, chainFiles)
	}

	if deps.Agent != nil {
		taskexecapi.AddRoutes(mux, deps.Agent, deps.Auth, stateSvc, deps.Defaults)
	}

	if deps.Agent != nil && chains != nil {
		compatapi.AddOpenAIRoutes(mux, compatapi.CompatDeps{
			Agent:        deps.Agent,
			Chains:       chains,
			StateService: stateSvc,
			Defaults:     deps.Defaults,
			Auth:         deps.Auth,
			Token:        config.Token,
		})
	}

	if deps.TerminalService != nil {
		var terminalAuth func(string) bool
		if strings.TrimSpace(config.Token) != "" {
			token := config.Token
			terminalAuth = func(cred string) bool { return AuthenticateCredential(token, cred) }
		}
		terminalapi.AddRoutes(mux, deps.TerminalService, deps.Auth, deps.TerminalEnabled, terminalAuth)
	}

	if deps.ToolsProviderService != nil {
		toolsapi.AddRemoteToolsRoutes(mux, deps.ToolsProviderService)
	}

	if deps.Fleet != nil {
		fleetapi.AddRoutes(mux, deps.Fleet)
	}

	if deps.Missions != nil {
		missionapi.AddRoutes(mux, deps.Missions)
	}

	if deps.MissionChanges != nil {
		missionchangesapi.AddRoutes(mux, deps.MissionChanges)
	}

	if deps.OperatorInbox != nil {
		operatorinboxapi.AddRoutes(mux, deps.OperatorInbox)
	}

	if deps.HITL != nil {
		approvalapi.AddRoutes(mux, deps.HITL)
	}

	if deps.PubSub != nil {
		taskeventsapi.AddRoutes(mux, deps.PubSub, deps.Auth)
		mcpSvc := mcpserverservice.New(deps.DB, mcpserverservice.WithUIBaseURL(config.UIBaseURL))
		mcpserverapi.AddMCPServerRoutes(mux, mcpSvc, deps.PubSub, deps.Auth)
	}
	return nil
}

func Handler(mux *http.ServeMux, config *Config) http.Handler {
	if config == nil {
		config = &Config{}
	}
	cors := &middleware.CORSConfig{
		AllowedAPIOrigins: firstNonEmpty(config.AllowedAPIOrigins, middleware.DefaultAllowedAPIOrigins),
		AllowedMethods:    middleware.DefaultAllowedMethods,
		AllowedHeaders:    middleware.DefaultAllowedHeaders,
		ProxyOrigin:       config.ProxyOrigin,
	}

	var h http.Handler = mux
	h = ProtectAPI(config.Token, config.AllowedAPIOrigins, h)
	h = apiframework.TracingMiddleware(h)
	h = apiframework.RequestIDMiddleware(h)
	h = middleware.EnableCORS(cors, h)
	return h
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func LoadConfig[T any](cfg *T) error {
	if cfg == nil {
		return fmt.Errorf("config pointer is nil")
	}
	config := map[string]string{}
	for _, kvPair := range os.Environ() {
		ar := strings.SplitN(kvPair, "=", 2)
		if len(ar) < 2 {
			continue
		}
		config[strings.ToLower(ar[0])] = ar[1]
	}
	b, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal env vars: %w", err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("failed to unmarshal into config struct: %w", err)
	}
	return nil
}
