package inferencesetup

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/mediageneration"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// RenderMedia uses the enabled role's verified model and persisted device allocation.
func (s *Service) RenderMedia(ctx context.Context, options mediageneration.Options) (transport.MediaResult, error) {
	role := RoleImage
	if options.Video {
		role = RoleVideo
	}
	status, err := s.VerifiedRole(ctx, role)
	if err != nil {
		return transport.MediaResult{}, err
	}
	if options.Model != "" && options.Model != status.Model {
		return transport.MediaResult{}, fmt.Errorf("%s uses %s; change its assignment in Local AI", status.Name, status.Model)
	}
	options.Digest, err = verifiedMediaDigest(status.Model, status.Verification.NativeExecutions)
	if err != nil {
		return transport.MediaResult{}, err
	}
	return s.renderNative(ctx, status.Assignment, options, false)
}

func (s *Service) renderNative(ctx context.Context, assignment Assignment, options mediageneration.Options, verify bool) (transport.MediaResult, error) {
	if modeldconn.WorkersFromContext(ctx) == nil {
		workers, err := modelworkers.NewConfigured(ctx, s.db)
		if err != nil {
			return transport.MediaResult{}, err
		}
		defer workers.Close()
		ctx = modeldconn.WithWorkers(ctx, workers)
	}
	if _, proposed := modeldconn.ProposedWorkerOptions(ctx, assignment.Role); !proposed {
		settings, err := modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(s.db.WithoutTransaction()), assignment.Role)
		if err != nil {
			return transport.MediaResult{}, err
		}
		ctx, err = modeldconn.WithWorkerOptions(ctx, assignment.Role, settings)
		if err != nil {
			return transport.MediaResult{}, err
		}
	}
	options.Model = assignment.Model
	if verify {
		return mediageneration.Verify(ctx, options)
	}
	return mediageneration.Render(ctx, options)
}

func verifiedMediaDigest(model string, reports []transport.SessionExecution) (string, error) {
	var digest string
	for _, report := range reports {
		if report.ModelName != model || report.Backend != "openvino" {
			continue
		}
		if report.Digest == "" || digest != "" && digest != report.Digest {
			return "", fmt.Errorf("%w: verify the media role for %s again", transport.ErrDigestMismatch, model)
		}
		digest = report.Digest
	}
	if digest == "" {
		return "", fmt.Errorf("%w: missing verified model digest for %s", transport.ErrDigestMismatch, model)
	}
	return digest, nil
}
