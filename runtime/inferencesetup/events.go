package inferencesetup

import (
	"context"
	"fmt"
)

// EmbeddingChangedSubject announces a committed embedding assignment or enablement change; consumers reread persisted configuration.
const EmbeddingChangedSubject = "inferencesetup.events.embedding_changed"

func (s *Service) embeddingChanged(ctx context.Context) error {
	if err := s.bus.Publish(context.WithoutCancel(ctx), EmbeddingChangedSubject, []byte(`{}`)); err != nil {
		return fmt.Errorf("embedding configuration saved but notification failed: %w", err)
	}
	return nil
}
