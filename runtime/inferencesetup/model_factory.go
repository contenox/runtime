package inferencesetup

import (
	"context"
	"log/slog"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/ollamatokenizer"
	"github.com/contenox/runtime/runtime/runtimestate"
)

// RuntimeModelFactory builds a short-lived model runtime from current persisted provider configuration.
type RuntimeModelFactory struct {
	DB      libdbexec.DBManager
	Bus     libbus.Messenger
	State   *runtimestate.State
	Tracker libtracker.ActivityTracker
}

// Models returns a model repository that observes the factory's current runtime state.
func (f RuntimeModelFactory) Models(ctx context.Context) (llmrepo.ModelRepo, func(), error) {
	workers, err := modelworkers.NewConfigured(ctx, f.DB)
	if err != nil {
		return nil, nil, err
	}
	closeWorkers := func() {
		if err := workers.Close(); err != nil {
			slog.Error("closing Modeld verification client", "error", err)
		}
	}
	models, err := llmrepo.NewModelManager(f.State, ollamatokenizer.NewEstimateTokenizer(), llmrepo.ModelManagerConfig{Workers: workers}, f.Tracker)
	if err != nil {
		closeWorkers()
		return nil, nil, err
	}
	return models, closeWorkers, nil
}
