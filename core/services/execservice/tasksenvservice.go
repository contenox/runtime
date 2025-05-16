package execservice

import (
	"context"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/taskengine"
	"github.com/js402/cate/libs/libdb"
)

type TasksEnvService struct {
	environmentExec taskengine.EnvExecutor
	db              libdb.DBManager
}

func NewTasksEnv(ctx context.Context, environmentExec taskengine.EnvExecutor, dbInstance libdb.DBManager) *TasksEnvService {
	return &TasksEnvService{
		environmentExec: environmentExec,
		db:              dbInstance,
	}
}

func (s *TasksEnvService) Execute(ctx context.Context, chain *taskengine.ChainDefinition, input string) (any, error) {
	tx := s.db.WithoutTransaction()

	storeInstance := store.New(tx)
	//TODO: check permission view? why not exec?
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionView); err != nil {
		return nil, err
	}

	return s.environmentExec.ExecEnv(ctx, chain, input)
}

func (s *TasksEnvService) GetServiceName() string {
	return "taskenviromentservice"
}

func (s *TasksEnvService) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
