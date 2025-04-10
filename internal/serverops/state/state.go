package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"

	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/libs/libbus"
	"github.com/js402/CATE/libs/libdb"
	"github.com/ollama/ollama/api"
)

type LLMState struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Models       []string                `json:"models"`
	PulledModels []api.ListModelResponse `json:"pulledModels"`
	Backend      store.Backend           `json:"backend"`
	Error        string                  `json:"error,omitempty"`
}

type State struct {
	dbInstance      libdb.DBManager
	state           sync.Map
	psInstance      libbus.Messenger
	dwQueue         dwqueue
	securityEnabled bool
	jwtSecret       string
}

// TODO: implement pools feature
func New(dbInstance libdb.DBManager, psInstance libbus.Messenger) *State {
	return &State{
		dbInstance: dbInstance,
		state:      sync.Map{},
		dwQueue:    dwqueue{dbInstance: dbInstance},
		psInstance: psInstance,
	}
}

// RunBackendCycle synchronizes backends as before.
func (s *State) RunBackendCycle(ctx context.Context) error {
	return s.syncBackends(ctx)
}

// RunDownloadCycle continuously pops and processes download jobs.
// It processes one job at a time until the queue is empty.
func (s *State) RunDownloadCycle(ctx context.Context) error {
	item, err := s.dwQueue.pop(ctx)
	if err != nil {
		if err == libdb.ErrNotFound {
			return nil
		}
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // Ensure we clean up the context when done

	done := make(chan struct{})

	ch := make(chan []byte, 16)
	sub, err := s.psInstance.Stream(ctx, "queue_cancel", ch)
	if err != nil {
		log.Println("Error subscribing to queue_cancel:", err)
		return nil
	}
	go func() {
		defer func() {
			sub.Unsubscribe()
			close(done)
		}()
		for {
			select {
			case data, ok := <-ch:
				if !ok {
					return
				}
				var queueItem store.Job
				if err := json.Unmarshal(data, &queueItem); err != nil {
					log.Println("Error unmarshalling cancel message:", err)
					continue
				}
				if queueItem.ID == item.URL {
					cancel()
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Printf("Processing download job: %+v", item)
	err = s.dwQueue.downloadModel(ctx, *item, func(status store.Status) error {
		log.Printf("Download progress for model %s: %+v", item.Model, status)
		message, _ := json.Marshal(status)
		return s.psInstance.Publish(ctx, "model_download", message)
	})

	if err != nil {
		log.Printf("Error downloading model %s: %v", item.Model, err)
	}

	cancel() // Ensure any waiting goroutines can exit
	<-done   // Wait for the cancel watcher to finish cleanup

	return nil
}

// Get returns a copy of the current service state.
func (s *State) Get(ctx context.Context) map[string]LLMState {
	state := map[string]LLMState{}
	s.state.Range(func(key, value any) bool {
		backend, ok := value.(*LLMState)
		if !ok {
			log.Printf("invalid type in state: %T", value)
			return true
		}
		var backendCopy LLMState
		raw, err := json.Marshal(backend)
		if err != nil {
			log.Printf("failed to marshal backend: %v", err)
		}
		err = json.Unmarshal(raw, &backendCopy)
		if err != nil {
			log.Printf("failed to unmarshal backend: %v", err)
		}
		state[backend.ID] = backendCopy
		return true
	})
	return state
}

// syncBackends synchronizes the list of backends and declared models.
func (s *State) syncBackends(ctx context.Context) error {
	tx := s.dbInstance.WithoutTransaction()

	backends, err := store.New(tx).ListBackends(ctx)
	if err != nil {
		return fmt.Errorf("fetching backends: %v", err)
	}

	models, err := store.New(tx).ListModels(ctx)
	if err != nil {
		return fmt.Errorf("fetching models: %v", err)
	}

	currentIDs := make(map[string]struct{})
	for _, backend := range backends {
		currentIDs[backend.ID] = struct{}{}
		s.processBackend(ctx, backend, models)
	}
	// Remove deleted backends from state.
	s.state.Range(func(key, value any) bool {
		id, ok := key.(string)
		if !ok {
			err = fmt.Errorf("BUG: invalid key type: %T %v", key, key)
			log.Printf("BUG: %v", err)
			return true
		}
		if _, exists := currentIDs[id]; !exists {
			s.state.Delete(id)
		}
		return true
	})
	return nil
}

func (s *State) processBackend(ctx context.Context, backend *store.Backend, declaredOllamaModels []*store.Model) {
	switch backend.Type {
	case "Ollama":
		s.processOllamaBackend(ctx, backend, declaredOllamaModels)
	default:
		log.Printf("Unsupported backend type: %s", backend.Type)
		brokenService := &LLMState{
			ID:      backend.ID,
			Name:    backend.Name,
			Models:  []string{},
			Backend: *backend,
			Error:   "Unsupported backend type: " + backend.Type,
		}
		s.state.Store(backend.ID, brokenService)
	}
}

func (s *State) processOllamaBackend(ctx context.Context, backend *store.Backend, declaredOllamaModels []*store.Model) {
	log.Printf("Processing Ollama backend for ID %s with declared models: %+v", backend.ID, declaredOllamaModels)

	models := []string{}
	for _, model := range declaredOllamaModels {
		models = append(models, model.Model)
	}
	log.Printf("Extracted model names for backend %s: %v", backend.ID, models)

	u, err := url.Parse(backend.BaseURL)
	if err != nil {
		log.Printf("Error parsing URL for backend %s: %v", backend.ID, err)
		stateservice := &LLMState{
			ID:           backend.ID,
			Name:         backend.Name,
			Models:       models,
			PulledModels: nil,
			Backend:      *backend,
			Error:        "Invalid URL: " + err.Error(),
		}
		s.state.Store(backend.ID, stateservice)
		return
	}
	log.Printf("Parsed URL for backend %s: %s", backend.ID, u.String())

	client := api.NewClient(u, http.DefaultClient)
	existingModels, err := client.List(ctx)
	if err != nil {
		log.Printf("Error listing models for backend %s: %v", backend.ID, err)
		stateservice := &LLMState{
			ID:           backend.ID,
			Name:         backend.Name,
			Models:       models,
			PulledModels: nil,
			Backend:      *backend,
			Error:        err.Error(),
		}
		s.state.Store(backend.ID, stateservice)
		return
	}
	log.Printf("Existing models from backend %s: %+v", backend.ID, existingModels.Models)

	declaredModelSet := make(map[string]struct{})
	for _, declaredModel := range declaredOllamaModels {
		declaredModelSet[declaredModel.Model] = struct{}{}
	}
	log.Printf("Declared model set for backend %s: %v", backend.ID, declaredModelSet)

	existingModelSet := make(map[string]struct{})
	for _, existingModel := range existingModels.Models {
		existingModelSet[existingModel.Model] = struct{}{}
	}
	log.Printf("Existing model set for backend %s: %v", backend.ID, existingModelSet)

	// For each declared model missing from the backend, add a download job.
	for declaredModel := range declaredModelSet {
		if _, ok := existingModelSet[declaredModel]; !ok {
			log.Printf("Model %s is declared but missing in backend %s. Adding to download queue.", declaredModel, backend.ID)
			err := s.dwQueue.add(ctx, *u, declaredModel)
			if err != nil {
				log.Printf("Error adding model %s to download queue: %v", declaredModel, err)
			}
		}
	}

	// For each model in the backend that is not declared, trigger deletion.
	for existingModel := range existingModelSet {
		if _, ok := declaredModelSet[existingModel]; !ok {
			log.Printf("Model %s exists in backend %s but is not declared. Triggering deletion.", existingModel, backend.ID)
			err := client.Delete(ctx, &api.DeleteRequest{
				Model: existingModel,
			})
			if err != nil {
				log.Printf("Error deleting model %s for backend %s: %v", existingModel, backend.ID, err)
			} else {
				log.Printf("Successfully deleted model %s for backend %s", existingModel, backend.ID)
			}
		}
	}

	modelResp, err := client.List(ctx)
	if err != nil {
		log.Printf("Error listing running models for backend %s after deletion: %v", backend.ID, err)
		stateservice := &LLMState{
			ID:           backend.ID,
			Name:         backend.Name,
			Models:       models,
			PulledModels: nil,
			Backend:      *backend,
			Error:        err.Error(),
		}
		s.state.Store(backend.ID, stateservice)
		return
	}
	log.Printf("Updated model list for backend %s: %+v", backend.ID, modelResp.Models)

	stateservice := &LLMState{
		ID:           backend.ID,
		Name:         backend.Name,
		Models:       models,
		PulledModels: modelResp.Models,
		Backend:      *backend,
	}
	s.state.Store(backend.ID, stateservice)
	log.Printf("Stored updated state for backend %s", backend.ID)
}

func FindModel(ctx context.Context, state *State, selectedModel string) (*LLMState, error) {
	for _, backendState := range state.Get(ctx) {
		for _, model := range backendState.PulledModels {
			if selectedModel == model.Model {
				return &backendState, nil
			}
		}
	}
	return nil, fmt.Errorf("the selected Model is not ready for usage")
}
