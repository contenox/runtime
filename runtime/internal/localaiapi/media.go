package localaiapi

import (
	"context"
	"net/http"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (h *handler) mediaDevices(w http.ResponseWriter, r *http.Request) {
	ctx := modeldconn.WithDataRoot(r.Context(), h.root)
	endpoint, err := modeldconn.LocalEndpointAddr(ctx)
	if err != nil {
		fail(w, 503, err)
		return
	}
	inventory, err := modeldconn.InventoryTarget(ctx, modeldconn.ModeldTarget{Endpoint: endpoint})
	if err != nil {
		fail(w, 503, err)
		return
	}
	for _, backend := range inventory.Backends {
		if backend.Backend == "openvino" && backend.State == "available" {
			reply(w, 200, backend.Devices)
			return
		}
	}
	reply(w, 200, []transport.DeviceInfo{})
}

func (h *handler) useForMedia(ctx context.Context, model model, connection string, target transport.ExecutionTarget) error {
	role := transport.MediaRoleForPipeline(model.Pipeline)
	options, err := modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(h.db.WithoutTransaction()), role)
	if err != nil {
		return err
	}
	options.Target = target.Normalize()
	proposal := inferencesetup.RouteProposal{
		Assignment:   inferencesetup.Assignment{Role: role, Model: model.Name, Provider: runtimestate.ConnectionSelector(connection)},
		RoleRevision: model.mediaRevision, SettingsChange: inferencesetup.ReplaceSettings, Settings: options,
	}
	reviewed, err := h.setup.Review(ctx, []inferencesetup.RouteProposal{proposal})
	if err != nil {
		return err
	}
	if _, err = h.setup.Apply(ctx, reviewed, inferencesetup.SourceUser); err != nil {
		return err
	}
	return h.setup.SetRoleEnabled(ctx, role, true)
}
