package execapi

import (
	"net/http"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/execservice"
)

func AddExecRoutes(mux *http.ServeMux, _ *serverops.Config, taskService *execservice.Service) {
	f := &taskManager{
		service: taskService,
	}
	mux.HandleFunc("POST /execute", f.execute)
}

type taskManager struct {
	service *execservice.Service
}

func (tm *taskManager) execute(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[execservice.TaskRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}

	resp, err := tm.service.Execute(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ExecuteOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}
