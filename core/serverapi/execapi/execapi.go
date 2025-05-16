package execapi

import (
	"net/http"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/execservice"
)

func AddExecRoutes(mux *http.ServeMux, _ *serverops.Config, promptService *execservice.ExecService, taskService *execservice.TasksEnvService) {
	f := &taskManager{
		promptService: promptService,
		taskService:   taskService,
	}
	mux.HandleFunc("POST /execute", f.execute)
	mux.HandleFunc("POST /tasks", f.tasks)
}

type taskManager struct {
	promptService *execservice.ExecService
	taskService   *execservice.TasksEnvService
}

func (tm *taskManager) execute(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[execservice.TaskRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}

	resp, err := tm.promptService.Execute(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

func (tm *taskManager) tasks(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[execservice.TaskRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}

	resp, err := tm.promptService.Execute(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}
