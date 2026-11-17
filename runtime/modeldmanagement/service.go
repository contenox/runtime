// Package modeldmanagement owns installation and status inspection of the local inference worker.
package modeldmanagement

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/contenox/runtime/runtime/servicecontract"
)

// Action selects a modeld management operation.
type Action string

const (
	// ActionDescribe returns the transport-neutral contract.
	ActionDescribe Action = "describe"
	// ActionInstall installs a compatible local worker.
	ActionInstall Action = "install"
	// ActionStatus probes the installed local worker.
	ActionStatus Action = "status"
)

// Contract describes modeld management for every product surface.
var Contract = servicecontract.Contract{
	ID: "modeld-management", Summary: "Install or inspect the local inference worker.",
	Description: "The same managed worker installation is used by automatic IDE setup and direct runtime clients.",
	Operations: []servicecontract.Operation{
		{ID: string(ActionDescribe), Summary: "Describe local worker management."},
		{ID: string(ActionInstall), Summary: "Install a compatible verified worker.", Inputs: []servicecontract.Input{{ID: "backend", Summary: "Required inference backend.", Type: "string", Required: true}}},
		{ID: string(ActionStatus), Summary: "Probe the worker lease and inference endpoint."},
	},
}

// Service manages the local inference worker.
type Service interface {
	Install(context.Context, string, io.Writer) (modeldinstall.Result, error)
	Status(context.Context) modeldprobe.Status
}

type service struct {
	dataRoot      string
	baseURL       string
	clientVersion string
}

// New constructs modeld management for one installation.
func New(dataRoot, baseURL, clientVersion string) (Service, error) {
	if strings.TrimSpace(clientVersion) == "" {
		return nil, errors.New("client version is required")
	}
	return &service{dataRoot: dataRoot, baseURL: baseURL, clientVersion: clientVersion}, nil
}

func (s *service) Install(ctx context.Context, backend string, progress io.Writer) (modeldinstall.Result, error) {
	if backend != "llama" && backend != "openvino" {
		return modeldinstall.Result{}, fmt.Errorf("unsupported modeld backend %q", backend)
	}
	if progress == nil {
		return modeldinstall.Result{}, errors.New("modeld install progress writer is required")
	}
	return modeldinstall.EnsureInstalled(ctx, backend, modeldinstall.Options{DataRoot: s.dataRoot, BaseURL: s.baseURL, ClientVersion: s.clientVersion, Progress: progress})
}

func (s *service) Status(ctx context.Context) modeldprobe.Status {
	return modeldprobe.New(s.dataRoot).Probe(ctx)
}

// Request selects one modeld management operation.
type Request struct {
	Action   Action
	Backend  string
	Progress io.Writer
}

// Result carries a modeld management result.
type Result struct {
	Contract *servicecontract.Contract `json:"contract,omitempty"`
	Install  *modeldinstall.Result     `json:"install,omitempty"`
	Status   *modeldprobe.Status       `json:"status,omitempty"`
}

// Execute validates and invokes one modeld management operation.
func Execute(ctx context.Context, service Service, request Request) (*Result, error) {
	if service == nil {
		return nil, errors.New("modeld management service is required")
	}
	switch request.Action {
	case ActionDescribe:
		contract := Contract
		return &Result{Contract: &contract}, nil
	case ActionInstall:
		installed, err := service.Install(ctx, request.Backend, request.Progress)
		if err != nil {
			return nil, err
		}
		return &Result{Install: &installed}, nil
	case ActionStatus:
		status := service.Status(ctx)
		return &Result{Status: &status}, nil
	default:
		return nil, fmt.Errorf("unknown modeld management action %q", request.Action)
	}
}
