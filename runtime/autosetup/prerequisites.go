package autosetup

import (
	"context"
	"errors"
	"strings"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
)

// NativePrerequisite selects an explicitly requested local backend installation.
type NativePrerequisite struct {
	Backend string `json:"backend"`
}

// PrepareLocal installs the selected native backend and registers an unambiguous local connection without assigning roles.
func (s *Service) PrepareLocal(ctx context.Context, request NativePrerequisite, opts Options, setup *inferencesetup.Service) (inferencesetup.ProviderResult, error) {
	if setup == nil || opts.Report == nil || opts.Progress == nil {
		return inferencesetup.ProviderResult{}, errors.New("native preparation requires configuration and progress reporting")
	}
	if request.Backend != "llama" && request.Backend != "openvino" {
		return inferencesetup.ProviderResult{}, errors.New("select llama.cpp or OpenVINO for local installation")
	}
	if err := report(opts, Progress{Phase: "engine", Message: "Installing " + request.Backend + " for this computer"}); err != nil {
		return inferencesetup.ProviderResult{}, err
	}
	writer := &prerequisiteProgress{report: opts.Report}
	if err := ensureWorker(ctx, dataRoot(opts.DataRoot), request.Backend, opts.WorkerBaseURL, writer); err != nil {
		return inferencesetup.ProviderResult{}, err
	}
	if writer.err != nil {
		return inferencesetup.ProviderResult{}, writer.err
	}
	configuration, err := setup.Configuration(ctx)
	if err != nil {
		return inferencesetup.ProviderResult{}, err
	}
	result := inferencesetup.ProviderResult{}
	for _, connection := range configuration.Providers {
		if nativeConnection(connection) && connection.URL == modeldconn.LocalSentinel {
			if result.ID != "" {
				return result, errors.New("native backend installed; more than one local connection is saved; " + remedy.PickConnection)
			}
			result.ID = connection.ID
		}
	}
	if result.ID == "" {
		result, err = setup.SaveProvider(ctx, inferencesetup.ProviderRequest{Name: "Local AI", Provider: "modeld", Endpoint: modeldconn.LocalSentinel, CredentialSource: "none"})
		if err != nil {
			return result, err
		}
	}
	if err := setup.RefreshProvider(ctx, runtimestate.ConnectionSelector(result.ID)); err != nil {
		result.DiscoveryError = err.Error()
	}
	return result, nil
}

type prerequisiteProgress struct {
	report func(Progress) error
	err    error
}

func (p *prerequisiteProgress) Write(value []byte) (int, error) {
	if p.err != nil {
		return 0, p.err
	}
	p.err = p.report(Progress{Phase: "engine", Message: strings.TrimSpace(string(value))})
	if p.err != nil {
		return 0, p.err
	}
	return len(value), nil
}
