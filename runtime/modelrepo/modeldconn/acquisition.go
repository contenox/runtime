package modeldconn

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/contenox/runtime/runtime/modelregistry"
)

// AcquireModel stages a complete local bundle and installs it through its shared owner.
func AcquireModel(ctx context.Context, registry modelregistry.Registry, name string, options modelstore.AutoPullOptions) error {
	if ctx == nil || options.DataRoot == "" {
		return errors.New("model acquisition requires context and data root")
	}
	ctx = WithDataRoot(ctx, options.DataRoot)
	options.Receive = func(ctx context.Context, manifest transport.PushManifest, reader io.Reader) (transport.PushResult, error) {
		status, err := readyDaemonWithProbe(ctx, modeldprobe.New(options.DataRoot))
		if err != nil {
			return transport.PushResult{}, err
		}
		if status.Backend != "shared" {
			return transport.PushResult{}, errors.New("model acquisition requires a shared owner; " + remedy.UpdateLocal)
		}
		client, err := Endpoint(ctx, "acquisition:"+options.DataRoot, status.Endpoint)
		if err != nil {
			return transport.PushResult{}, err
		}
		return client.PushModel(ctx, manifest, reader)
	}
	return modelstore.EnsureModelAvailable(ctx, registry, name, options)
}

// AcquireModelAt stages a bundle outside the local model store and sends it to the selected remote owner.
func AcquireModelAt(ctx context.Context, registry modelregistry.Registry, name, endpoint string, options modelstore.AutoPullOptions) (err error) {
	if ctx == nil || registry == nil || options.DataRoot == "" || endpoint == "" || endpoint == LocalSentinel {
		return errors.New("remote model acquisition requires context, registry, staging root and remote endpoint")
	}
	client, err := Endpoint(ctx, "acquisition:"+endpoint, endpoint)
	if err != nil {
		return err
	}
	if client.Backend != "shared" {
		return errors.New("model acquisition requires a shared owner; update and restart the selected modeld connection")
	}
	if err := os.MkdirAll(options.DataRoot, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(options.DataRoot, ".remote-acquisition-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	options.DataRoot = staging
	options.Receive = client.PushModel
	return modelstore.EnsureModelAvailable(ctx, registry, name, options)
}
