package grpc

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
)

type admissionReq struct {
	Action   string             `json:"action"`
	Policy   admission.Config   `json:"policy"`
	Permit   admission.Permit   `json:"permit"`
	Ceilings map[string]float64 `json:"ceilings,omitempty"`
}

type ceilingController interface {
	ConfigureCeilings(context.Context, map[string]float64) error
}

type admissionController interface {
	Admission(context.Context) (admission.Snapshot, error)
	ConfigureAdmission(context.Context, admission.Config) error
}

func (s *Server) admission(ctx context.Context, req *admissionReq) (*admission.Snapshot, error) {
	if err := checkFence(ctx, s.instanceID); err != nil {
		return nil, encodeError(err)
	}
	controller, ok := s.svc.(admissionController)
	if !ok {
		return nil, encodeError(transport.ErrUnsupportedFeature)
	}
	switch req.Action {
	case "read":
	case "configure":
		if err := controller.ConfigureAdmission(ctx, req.Policy); err != nil {
			return nil, encodeError(err)
		}
	case "configure-ceilings":
		ceilings, ok := s.svc.(ceilingController)
		if !ok {
			return nil, encodeError(transport.ErrUnsupportedFeature)
		}
		if req.Ceilings == nil {
			return nil, fmt.Errorf("configure-ceilings requires a ceilings map")
		}
		if err := ceilings.ConfigureCeilings(ctx, req.Ceilings); err != nil {
			return nil, encodeError(err)
		}
	case "wait-yield", "resume":
		yielding, ok := s.svc.(admission.YieldService)
		if !ok {
			return nil, encodeError(transport.ErrUnsupportedFeature)
		}
		var err error
		if req.Action == "wait-yield" {
			err = yielding.WaitYield(ctx, req.Permit)
		} else {
			err = yielding.ResumeAtBoundary(ctx, req.Permit)
		}
		if err != nil {
			return nil, encodeError(err)
		}
		return &admission.Snapshot{}, nil
	default:
		return nil, fmt.Errorf("unknown admission action %q", req.Action)
	}
	snapshot, err := controller.Admission(ctx)
	if err != nil {
		return nil, encodeError(err)
	}
	return &snapshot, nil
}

// Admission reads the shared owner's admission state.
func (c *Client) Admission(ctx context.Context) (admission.Snapshot, error) {
	var result admission.Snapshot
	err := c.invoke(ctx, "Admission", &admissionReq{Action: "read"}, &result)
	return result, err
}

// ConfigureAdmission updates the existing owner scheduler without replacing it.
func (c *Client) ConfigureAdmission(ctx context.Context, policy admission.Config) error {
	return c.invoke(ctx, "Admission", &admissionReq{Action: "configure", Policy: policy}, new(admission.Snapshot))
}

// ConfigureCeilings replaces the shared owner's device ceilings, as pool id to a
// share of the pool's total bytes; the empty map removes every ceiling.
func (c *Client) ConfigureCeilings(ctx context.Context, ceilings map[string]float64) error {
	if ceilings == nil {
		return fmt.Errorf("device ceilings require a map; an empty map removes every ceiling")
	}
	return c.invoke(ctx, "Admission", &admissionReq{Action: "configure-ceilings", Ceilings: ceilings}, new(admission.Snapshot))
}

// WaitYield waits for a safe-boundary request without suspending native execution.
func (c *Client) WaitYield(ctx context.Context, permit admission.Permit) error {
	return c.invoke(ctx, "Admission", &admissionReq{Action: "wait-yield", Permit: permit}, new(admission.Snapshot))
}

// ResumeAtBoundary acknowledges suspension and waits for the same permit to resume.
func (c *Client) ResumeAtBoundary(ctx context.Context, permit admission.Permit) error {
	return c.invoke(ctx, "Admission", &admissionReq{Action: "resume", Permit: permit}, new(admission.Snapshot))
}
