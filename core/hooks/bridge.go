package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/contenox/runtime/runtimesdk"
	"github.com/contenox/runtime/runtimetypes"
	"github.com/contenox/runtime/taskengine"
)

type BridgeService struct {
	Hooks   map[string]taskengine.HookRepo
	Client  *runtimesdk.Client
	BaseURL string // Gateway's internal URL (e.g., http://gateway:8080)
}

func NewBridgeService(hooks map[string]taskengine.HookRepo, client *runtimesdk.Client, baseURL string) *BridgeService {
	return &BridgeService{
		Hooks:   hooks,
		Client:  client,
		BaseURL: baseURL,
	}
}

// Start registers hooks and creates HTTP handler
func (b *BridgeService) Start(ctx context.Context) error {

	// Register hooks with runtime
	err := b.registerHooks(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (b *BridgeService) HandleHook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "Hook name missing in URL", http.StatusBadRequest)
		return
	}

	hook, exists := b.Hooks[name]
	if !exists {
		http.NotFound(w, r)
		return
	}

	var req struct {
		StartingTime time.Time            `json:"startingTime"`
		Input        any                  `json:"input"`
		DataType     string               `json:"dataType"`
		Transition   string               `json:"transition"`
		Args         *taskengine.HookCall `json:"args"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	dataType, err := taskengine.DataTypeFromString(req.DataType)
	if err != nil {
		http.Error(w, "Invalid data type", http.StatusBadRequest)
		return
	}

	// Execute hook
	output, outType, transition, err := hook.Exec(
		r.Context(),
		req.StartingTime,
		req.Input,
		dataType,
		req.Transition,
		req.Args,
	)

	// Prepare response
	resp := struct {
		Output     any    `json:"output"`
		DataType   string `json:"dataType"`
		Transition string `json:"transition"`
		Error      string `json:"error,omitempty"`
	}{
		Output:     output,
		DataType:   outType.String(),
		Transition: transition,
		Error:      err.Error(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (b *BridgeService) makeHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hook := b.Hooks[name]

		var req struct {
			StartingTime time.Time            `json:"startingTime"`
			Input        any                  `json:"input"`
			DataType     string               `json:"dataType"`
			Transition   string               `json:"transition"`
			Args         *taskengine.HookCall `json:"args"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		dataType, err := taskengine.DataTypeFromString(req.DataType)
		if err != nil {
			http.Error(w, "Invalid data type", http.StatusBadRequest)
			return
		}

		// Execute hook
		output, outType, transition, err := hook.Exec(
			r.Context(),
			req.StartingTime,
			req.Input,
			dataType,
			req.Transition,
			req.Args,
		)

		// Prepare response
		resp := struct {
			Output     any    `json:"output"`
			DataType   string `json:"dataType"`
			Transition string `json:"transition"`
		}{
			Output:     output,
			DataType:   outType.String(),
			Transition: transition,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func (b *BridgeService) registerHooks(ctx context.Context) error {
	for name := range b.Hooks {
		hook := runtimetypes.RemoteHook{
			Name:        name,
			EndpointURL: fmt.Sprintf("%s/hooks/legacy/%s", b.BaseURL, name),
			Method:      "POST",
			TimeoutMs:   5000,
		}
		err := b.Client.HookService.Create(ctx, &hook)
		if err != nil {
			return fmt.Errorf("failed to register hook %s: %w", name, err)
		}
	}
	return nil
}
