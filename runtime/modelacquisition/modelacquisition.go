// Package modelacquisition owns curated native-model discovery and acquisition.
package modelacquisition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
)

// Contract describes model acquisition independently of the selected transport or host.
var Contract = servicecontract.Contract{
	ID: "model-acquisition", Summary: "List and download local models",
	Description: "List the curated local models with the roles each serves, then download one complete model bundle to this computer or to a selected modeld connection so a role can be assigned to and verified on it.",
	Operations: []servicecontract.Operation{
		{
			ID: "catalog", Summary: "List the downloadable local models",
			Description: "Read the curated local-model catalog with each model's size, the roles it can serve and whether it is already downloaded.",
			Spec:        "List the curated local models this computer can download. Each entry carries the model's name, its size, the roles it can serve and whether its bundle is already on this computer. The names are the identities the download operation accepts.",
			Output: servicecontract.Output{
				Stdout: "One entry per curated model: name, engine, size and description.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the catalog is read", Meaning: "every listed name can be downloaded"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the catalog cannot be read", Message: "names the failure"},
			},
		},
		{
			ID: "pull", Summary: "Acquire a complete model bundle",
			Description: "Download one curated model's complete bundle to the chosen host, register the local connection that serves it and rediscover that connection's models. Assigning and verifying the model for a role is a separate step.",
			Spec:        "Download one curated local model, with every file its engine needs, to this computer or to a selected modeld connection. The download reports its progress, can be stopped, and installs nothing until the whole bundle has arrived, so a stopped or failed download leaves no partial model and can simply be run again. Afterwards the connection that serves the model is registered if none exists and its models are rediscovered; a role is assigned to the model with its own command.",
			Usage:       "<name>",
			Inputs: []servicecontract.Input{
				{ID: "model", Summary: "Curated model identity, as the catalog lists it", Type: "string", Required: true, Positional: 1, Placeholder: "name"},
				{ID: "acquisition", Summary: "The host that receives the bundle: target local or connection, with the connection id for a connection; absent installs on this computer", Type: "object"},
			},
			Output: servicecontract.Output{
				Stdout: "The download's progress, then the model's name once it is available.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the complete bundle is installed", Meaning: "the model is discovered on its connection and can be assigned to a role"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the name is not in the catalog", Message: "names the unknown model and lists the catalog's names"},
				{When: "the host is not a reachable modeld owner", Message: "names the host and what it answered"},
				{When: "the download is stopped or fails", Message: "reports the cause and leaves nothing installed"},
			},
		},
	},
}

// ProviderSetup registers the local provider used after acquisition.
type ProviderSetup interface {
	ConnectProvider(context.Context, inferencesetup.ProviderRequest) (inferencesetup.ProviderResult, error)
}

// ProviderRefresher refreshes discovered models for selected connections.
type ProviderRefresher interface {
	RefreshProviderModels(context.Context, ...string) error
}

// Installer acquires curated models through local and remote model-store owners.
type Installer interface {
	Ensure(context.Context, modelregistry.Registry, string, modelstore.AutoPullOptions) error
	EnsureAt(context.Context, modelregistry.Registry, string, string, modelstore.AutoPullOptions) error
}

// Request selects the host that receives the complete model bundle.
type Request struct {
	Target       string `json:"target"`
	ConnectionID string `json:"connectionId"`
	Model        string `json:"model"`
}

// Progress receives download byte positions.
type Progress func(modelstore.DownloadProgress) error

// DownloadProgress is the byte position reported while acquiring a model.
type DownloadProgress = modelstore.DownloadProgress

// Service owns native-model discovery and acquisition.
type Service struct {
	db        libdbexec.DBManager
	setup     ProviderSetup
	state     ProviderRefresher
	registry  modelregistry.Registry
	installer Installer
	dataRoot  string
	tracker   libtracker.ActivityTracker
}

// New constructs model acquisition from required runtime dependencies.
func New(db libdbexec.DBManager, setup ProviderSetup, state ProviderRefresher, registry modelregistry.Registry, installer Installer, dataRoot string, tracker libtracker.ActivityTracker) (*Service, error) {
	if db == nil || setup == nil || state == nil || registry == nil || installer == nil || tracker == nil {
		return nil, errors.New("model acquisition requires database, setup, state, registry, installer, and tracker")
	}
	if strings.TrimSpace(dataRoot) == "" {
		return nil, errors.New("model acquisition data root is required")
	}
	return &Service{db: db, setup: setup, state: state, registry: registry, installer: installer, dataRoot: dataRoot, tracker: tracker}, nil
}

// UnknownModelError reports a model name the catalog does not carry, with the
// names it does.
type UnknownModelError struct {
	Model string
	Known []string
}

// Error implements error.
func (e *UnknownModelError) Error() string {
	return fmt.Sprintf("%q is not a downloadable model; the catalog carries: %s", e.Model, strings.Join(e.Known, ", "))
}

// LibraryEntry is one catalog model with the roles it serves and whether its
// bundle is already on this computer.
type LibraryEntry struct {
	Model      modelregistry.ModelDescriptor
	Roles      []string
	Downloaded bool
}

// Catalog returns the current curated local-model catalog.
func (s *Service) Catalog(ctx context.Context) ([]modelregistry.ModelDescriptor, error) {
	return s.registry.List(ctx)
}

// Library returns the catalog with the roles each model serves and whether its
// bundle is already in this computer's model store.
func (s *Service) Library(ctx context.Context) ([]LibraryEntry, error) {
	models, err := s.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	directory := modelstore.Dir(s.dataRoot, "")
	entries := make([]LibraryEntry, 0, len(models))
	for _, model := range models {
		_, resolveErr := modelstore.Resolve(directory, model.Name, model.BackendType(), "")
		if resolveErr != nil && !errors.Is(resolveErr, modelstore.ErrModelNotFound) {
			return nil, resolveErr
		}
		entries = append(entries, LibraryEntry{Model: model, Roles: inferencesetup.RolesServed(model), Downloaded: resolveErr == nil})
	}
	return entries, nil
}

func (s *Service) requireCatalogModel(ctx context.Context, model string) error {
	if _, err := s.registry.Resolve(ctx, model); err == nil {
		return nil
	} else if !errors.Is(err, modelregistry.ErrNotFound) {
		return err
	}
	models, err := s.registry.List(ctx)
	if err != nil {
		return err
	}
	known := make([]string, 0, len(models))
	for _, candidate := range models {
		known = append(known, candidate.Name)
	}
	return &UnknownModelError{Model: model, Known: known}
}

// Pull acquires one model, registers the local provider, and refreshes its discovered capabilities.
func (s *Service) Pull(ctx context.Context, model string, progress Progress) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("model is required")
	}
	if progress == nil {
		return errors.New("model acquisition progress sink is required")
	}
	if ctx == nil {
		return errors.New("model acquisition context is required")
	}
	ctx = modeldconn.WithDataRoot(ctx, s.dataRoot)
	if err := s.requireCatalogModel(ctx, model); err != nil {
		return err
	}
	if err := s.installer.Ensure(ctx, s.registry, model, modelstore.AutoPullOptions{
		DataRoot: s.dataRoot, ProgressOut: io.Discard, Progress: progress, Tracker: s.tracker,
	}); err != nil {
		return err
	}
	backends, err := backendservice.New(s.db).List(ctx, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return err
	}
	var local []string
	for _, backend := range backends {
		if (backend.Type == "modeld" || backend.Type == "local") && backend.BaseURL == "local" {
			local = append(local, runtimestate.ConnectionSelector(backend.ID))
		}
	}
	if len(local) == 0 {
		connection, err := s.setup.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "Local models", Provider: "modeld", Endpoint: "local"})
		if err != nil {
			return fmt.Errorf("model acquired; saving its local connection failed: %w", err)
		}
		if connection.DiscoveryError != "" {
			return fmt.Errorf("model acquired and connection %s saved; discovery failed: %s", connection.ID, connection.DiscoveryError)
		}
		return nil
	}
	if err := s.state.RefreshProviderModels(ctx, local...); err != nil {
		return fmt.Errorf("model acquired; local connection discovery failed: %w", err)
	}
	return nil
}

// PullTo acquires a model on the selected host and refreshes only its connection.
func (s *Service) PullTo(ctx context.Context, request Request, progress Progress) error {
	if ctx == nil || progress == nil || strings.TrimSpace(request.Model) == "" {
		return errors.New("model acquisition requires context, model and progress sink")
	}
	switch request.Target {
	case "local":
		if request.ConnectionID != "" {
			return errors.New("local model acquisition cannot select a connection")
		}
		return s.Pull(ctx, request.Model, progress)
	case "connection":
		if request.ConnectionID == "" {
			return errors.New("select a native model connection")
		}
		if err := s.requireCatalogModel(ctx, strings.TrimSpace(request.Model)); err != nil {
			return err
		}
	default:
		return errors.New("model acquisition requires an explicit local or connection target")
	}
	backend, err := backendservice.New(s.db).Get(ctx, request.ConnectionID)
	if err != nil {
		return err
	}
	if backend.Type != "modeld" && backend.Type != "local" || backend.BaseURL == "" {
		return errors.New("selected connection must be a configured modeld endpoint")
	}
	if backend.BaseURL == modeldconn.LocalSentinel {
		return s.Pull(ctx, request.Model, progress)
	}
	if err := s.installer.EnsureAt(ctx, s.registry, strings.TrimSpace(request.Model), backend.BaseURL, modelstore.AutoPullOptions{
		DataRoot: s.dataRoot, ProgressOut: io.Discard, Progress: progress, Tracker: s.tracker,
	}); err != nil {
		return err
	}
	current, err := backendservice.New(s.db).Get(ctx, request.ConnectionID)
	if err != nil {
		return fmt.Errorf("model acquired on the reviewed endpoint; connection changed: %w", err)
	}
	if current.Revision != backend.Revision {
		return errors.New("model acquired on the reviewed endpoint; connection changed during acquisition; review and refresh its models")
	}
	if err := s.state.RefreshProviderModels(ctx, runtimestate.ConnectionSelector(backend.ID)); err != nil {
		return fmt.Errorf("model acquired on connection %s; discovery failed: %w", backend.ID, err)
	}
	return nil
}

// StoreInstaller acquires models through the runtime model store.
type StoreInstaller struct{}

// Ensure acquires a model through the shared owner's model store.
func (StoreInstaller) Ensure(ctx context.Context, registry modelregistry.Registry, model string, options modelstore.AutoPullOptions) error {
	return modeldconn.AcquireModel(ctx, registry, model, options)
}

// EnsureAt acquires a model through the selected remote owner's model store.
func (StoreInstaller) EnsureAt(ctx context.Context, registry modelregistry.Registry, model, endpoint string, options modelstore.AutoPullOptions) error {
	return modeldconn.AcquireModelAt(ctx, registry, model, endpoint, options)
}
