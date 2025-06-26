package systemapi

import (
	"net/http"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
)

type systemRoutes struct {
	manager serverops.ServiceManager
}

func AddRoutes(mux *http.ServeMux, _ *serverops.Config, manager serverops.ServiceManager) {
	sr := systemRoutes{manager: manager}
	mux.HandleFunc("GET /system/services", sr.info)
	mux.HandleFunc("GET /system/resources", sr.resources)
}

func (sr *systemRoutes) info(w http.ResponseWriter, r *http.Request) {
	res, err := sr.manager.GetServices()
	if err != nil {
		serverops.Error(w, r, err, serverops.ListOperation)
		return
	}
	serviceNames := []string{}
	for _, sm := range res {
		serviceNames = append(serviceNames, sm.GetServiceName())
	}
	serverops.Encode(w, r, http.StatusOK, serviceNames)
}

func (sr *systemRoutes) resources(w http.ResponseWriter, r *http.Request) {
	serverops.Encode(w, r, http.StatusOK, store.ResourceTypes)
}
