package execservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
)

type Service struct {
	promptRepo llmrepo.ModelRepo
	db         libdb.DBManager
}

func New(ctx context.Context, promptRepo llmrepo.ModelRepo, dbInstance libdb.DBManager) *Service {
	return &Service{
		promptRepo: promptRepo,
		db:         dbInstance,
	}
}

type TaskRequest struct {
	Prompt string `json:"prompt"`
}

type TaskResponse struct {
	ID       string `json:"id"`
	Response string `json:"response"`
}

func (s *Service) Execute(ctx context.Context, request *TaskRequest) (*TaskResponse, error) {
	tx := s.db.WithoutTransaction()

	storeInstance := store.New(tx)
	//TODO: check permission view? why not exec?
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionView); err != nil {
		return nil, err
	}

	provider, err := s.promptRepo.GetProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	promptClient, err := llmresolver.ResolvePromptExecute(ctx, llmresolver.ResolvePromptRequest{
		ModelName: provider.ModelName(),
	}, s.promptRepo.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve prompt client: %w", err)
	}

	if promptClient == nil {
		return nil, errors.New("prompt client is nil")
	}

	response, err := promptClient.Prompt(ctx, request.Prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to execute prompt: %w", err)
	}

	return &TaskResponse{
		ID:       uuid.NewString(),
		Response: response,
	}, nil
}

func (s *Service) GetServiceName() string {
	return "tasksservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
