package modeldconn

import (
	"context"
	"errors"

	"github.com/contenox/runtime/internal/transport"
)

func executionEndpoint(ctx context.Context, target ModeldTarget) (EndpointClient, error) {
	workers := WorkersFromContext(ctx)
	if workers == nil || !workers.shared {
		return targetEndpoint(ctx, target.BackendID, target)
	}
	key := workers.owner
	if target.BackendID != workers.owner {
		key += "/endpoint/" + target.Endpoint
	}
	workers.mu.Lock()
	if workers.closed || workers.ctx.Err() != nil {
		workers.mu.Unlock()
		return EndpointClient{}, errors.New("native runtime client is closed")
	}
	if workers.clientEndpoints == nil {
		workers.clientEndpoints = map[string]bool{}
	}
	workers.clientEndpoints[key] = true
	workers.mu.Unlock()
	client, err := targetEndpoint(ctx, key, target)
	if err != nil {
		return EndpointClient{}, err
	}
	workers.mu.Lock()
	closed := workers.closed || workers.ctx.Err() != nil
	workers.mu.Unlock()
	if closed {
		CloseEndpoint(key)
		return EndpointClient{}, errors.New("native runtime client closed while connecting")
	}
	return client, nil
}

func targetEndpoint(ctx context.Context, key string, target ModeldTarget) (EndpointClient, error) {
	client, err := Endpoint(ctx, key, target.Endpoint)
	if err == nil && target.Instance != "" && target.Instance != client.InstanceID {
		return EndpointClient{}, transport.ErrStaleFence
	}
	return client, err
}
