package modeld

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

func openSessionWithAutoPull(ctx context.Context, target modeldconn.ModeldTarget, modelName string, expected modeldconn.ModelRef, cfg transport.Config, tracker libtracker.ActivityTracker) (transport.Session, error) {
	ref, err := resolveModelRef(ctx, target, modelName, expected)
	if err != nil {
		return nil, err
	}
	sess, err := modeldconn.OpenSessionTarget(ctx, target, ref, cfg)
	if err == nil {
		return captureSession(ctx, ref, sess)
	}

	if target.Endpoint == "" && isModelNotFoundError(err) {
		reg := modelregistry.New(nil)
		pullErr := modeldconn.AcquireModel(ctx, reg, modelName, modelstore.AutoPullOptions{
			DataRoot:    modeldconn.DataRootFromContext(ctx),
			ProgressOut: os.Stderr,
			Tracker:     tracker,
		})
		if pullErr != nil {
			return nil, fmt.Errorf("model %q not found locally and auto-pull failed: %w (original error: %v)", modelName, pullErr, err)
		}
		sess, err := modeldconn.OpenSessionTarget(ctx, target, ref, cfg)
		if err != nil {
			return nil, err
		}
		return captureSession(ctx, ref, sess)
	}

	return nil, err
}

func embedWithAutoPull(ctx context.Context, target modeldconn.ModeldTarget, modelName string, expected modeldconn.ModelRef, cfg transport.Config, text string, tracker libtracker.ActivityTracker) (transport.EmbedResult, error) {
	ref, err := resolveModelRef(ctx, target, modelName, expected)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	res, err := modeldconn.EmbedTarget(ctx, target, ref, cfg, text)
	if err == nil {
		return res, modeldconn.RecordExecution(ctx, ref, res.Execution)
	}

	if target.Endpoint == "" && isModelNotFoundError(err) {
		reg := modelregistry.New(nil)
		pullErr := modeldconn.AcquireModel(ctx, reg, modelName, modelstore.AutoPullOptions{
			DataRoot:    modeldconn.DataRootFromContext(ctx),
			ProgressOut: os.Stderr,
			Tracker:     tracker,
		})
		if pullErr != nil {
			return transport.EmbedResult{}, fmt.Errorf("model %q not found locally and auto-pull failed: %w (original error: %v)", modelName, pullErr, err)
		}
		res, err := modeldconn.EmbedTarget(ctx, target, ref, cfg, text)
		if err != nil {
			return res, err
		}
		return res, modeldconn.RecordExecution(ctx, ref, res.Execution)
	}

	return transport.EmbedResult{}, err
}

func captureSession(ctx context.Context, ref modeldconn.ModelRef, session transport.Session) (transport.Session, error) {
	if !modeldconn.CapturingExecution(ctx) {
		return session, nil
	}
	report := session.ExplainContext()
	if report.FatalError != "" || report.Closed {
		return nil, errors.Join(fmt.Errorf("native execution evidence unavailable: closed=%t error=%s", report.Closed, report.FatalError), session.Close())
	}
	if err := modeldconn.RecordExecution(ctx, ref, report.Execution); err != nil {
		return nil, errors.Join(err, session.Close())
	}
	return session, nil
}

func isModelNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, transport.ErrModelNotFound) || errors.Is(err, modelstore.ErrModelNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "model not found") || strings.Contains(msg, "ModelNotFound")
}

func resolveModelRef(ctx context.Context, target modeldconn.ModeldTarget, name string, expected modeldconn.ModelRef) (modeldconn.ModelRef, error) {
	if expected.Type != "" {
		if expected.Name != name {
			return modeldconn.ModelRef{}, errors.New("model identity differs from the observed route; " + remedy.ReplanRole)
		}
		return expected, nil
	}
	if desc, err := modelregistry.New(nil).Resolve(ctx, name); err == nil {
		return modeldconn.ModelRef{Name: name, Type: desc.BackendType(), Digest: expected.Digest}, nil
	} else if !errors.Is(err, modelregistry.ErrNotFound) {
		return modeldconn.ModelRef{}, err
	}
	endpoint := target.Endpoint
	if endpoint == "" {
		var err error
		endpoint, err = modeldconn.LocalEndpointAddr(ctx)
		if err != nil {
			return modeldconn.ModelRef{}, err
		}
	}
	client, err := modeldconn.Endpoint(ctx, target.BackendID, endpoint)
	if err != nil {
		return modeldconn.ModelRef{}, err
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return modeldconn.ModelRef{}, err
	}
	for _, model := range models {
		if model.Name == name {
			if expected.Digest != "" && model.Digest != expected.Digest {
				return modeldconn.ModelRef{}, fmt.Errorf("%w: %s changed since discovery; "+remedy.ReplanRole, transport.ErrDigestMismatch, name)
			}
			return modeldconn.ModelRef{Name: name, Type: model.Type, Digest: model.Digest}, nil
		}
	}
	return modeldconn.ModelRef{}, fmt.Errorf("%w: %s is unavailable on the selected connection", transport.ErrModelNotFound, name)
}
