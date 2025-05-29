package execservice

import (
	"context"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/libs/libdb"
)

type TasksEnvService interface {
	Execute(ctx context.Context, chain *taskengine.ChainDefinition, input string) (any, error)
	serverops.ServiceMeta
}

type tasksEnvService struct {
	environmentExec taskengine.EnvExecutor
	db              libdb.DBManager
}

func NewTasksEnv(ctx context.Context, environmentExec taskengine.EnvExecutor, dbInstance libdb.DBManager) TasksEnvService {
	return &tasksEnvService{
		environmentExec: environmentExec,
		db:              dbInstance,
	}
}

func (s *tasksEnvService) Execute(ctx context.Context, chain *taskengine.ChainDefinition, input string) (any, error) {
	tx := s.db.WithoutTransaction()

	storeInstance := store.New(tx)
	//TODO: check permission view? why not exec?
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionView); err != nil {
		return nil, err
	}

	return s.environmentExec.ExecEnv(ctx, chain, input)
}

func (s *tasksEnvService) GetServiceName() string {
	return "taskenviromentservice"
}

func (s *tasksEnvService) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
