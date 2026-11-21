package modelstore

import (
	"context"
	"io"

	"github.com/contenox/runtime/internal/transport"
)

// NodeAdmin adapts local storage and its mutation protection to the daemon contract.
type NodeAdmin struct{ *Admin }

// NewNodeAdmin exposes the model store through the common node management contract.
func NewNodeAdmin(dir string) *NodeAdmin { return &NodeAdmin{NewAdmin(dir)} }

// NewProtectedNodeAdmin shares model acquisition and mutation protection with the owner.
func NewProtectedNodeAdmin(dir string, check func(string) error) (*NodeAdmin, error) {
	admin, err := NewProtectedAdmin(dir, check)
	if err != nil {
		return nil, err
	}
	return &NodeAdmin{admin}, nil
}

// ListModels lists installed artifacts without loading them.
func (a *NodeAdmin) ListModels(ctx context.Context) ([]transport.NodeModel, error) {
	models, err := a.Admin.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]transport.NodeModel, len(models))
	for i, model := range models {
		out[i] = transport.NodeModel{Name: model.Name, Type: model.Type, Digest: model.Digest, SizeBytes: model.SizeBytes, ContextLength: model.ContextLength, Pipeline: model.Pipeline, MediaError: model.MediaError}
	}
	return out, nil
}

// DiskStats reports the model store's filesystem capacity.
func (a *NodeAdmin) DiskStats(ctx context.Context) (transport.NodeDiskStats, error) {
	stats, err := a.Admin.DiskStats(ctx)
	if err != nil {
		return transport.NodeDiskStats{}, err
	}
	return transport.NodeDiskStats{FreeBytes: stats.FreeBytes, UsedBytes: stats.UsedBytes, TotalBytes: stats.TotalBytes}, nil
}

// ReceiveModel verifies and installs an incoming model through the shared mutation guard.
func (a *NodeAdmin) ReceiveModel(ctx context.Context, manifest transport.PushManifest, reader io.Reader) (transport.PushResult, error) {
	result, err := a.Admin.ReceiveModel(ctx, PushManifest{Name: manifest.Name, Type: manifest.Type, Digest: manifest.Digest, TotalBytes: manifest.TotalBytes, Format: PushFormat(manifest.Format)}, reader)
	if err != nil {
		return transport.PushResult{}, err
	}
	return transport.PushResult{AlreadyPresent: result.AlreadyPresent, BytesWritten: result.BytesWritten}, nil
}
