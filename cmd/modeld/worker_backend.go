package main

import (
	"fmt"
	"os"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
)

func selectWorkerBackend(name string, policy capacity.Policy, tracker libtracker.ActivityTracker) (transport.Service, string, error) {
	if name == "" {
		name = os.Getenv("CONTENOX_MODELD_BACKEND")
	}
	if name != "" {
		factory, ok := backends[name]
		if !ok {
			return nil, "", fmt.Errorf("modeld backend %q is not compiled in (available: %s)", name, availableBackends())
		}
		return factory(policy, tracker), name, nil
	}
	svc, backend := selectBackend(policy, tracker)
	return svc, backend, nil
}
