package taskengine

import (
	"context"
	"sync"

	"github.com/contenox/runtime/runtime/taskengine/llmretry"
)

type RetryOutcomeSink struct {
	mu       sync.Mutex
	outcomes []llmretry.Outcome
}

func (s *RetryOutcomeSink) Append(o llmretry.Outcome) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.outcomes = append(s.outcomes, o)
	s.mu.Unlock()
}

func (s *RetryOutcomeSink) Outcomes() []llmretry.Outcome {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llmretry.Outcome, len(s.outcomes))
	copy(out, s.outcomes)
	return out
}

func (s *RetryOutcomeSink) LastErrorClass() llmretry.ErrorClass {
	if s == nil {
		return llmretry.ClassNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outcomes) == 0 {
		return llmretry.ClassNone
	}
	return s.outcomes[len(s.outcomes)-1].LastErrorClass
}

type retryOutcomeSinkKey struct{}

func WithRetryOutcomeSink(ctx context.Context, sink *RetryOutcomeSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, retryOutcomeSinkKey{}, sink)
}

func retryOutcomeSinkFromContext(ctx context.Context) *RetryOutcomeSink {
	v, _ := ctx.Value(retryOutcomeSinkKey{}).(*RetryOutcomeSink)
	return v
}

func appendRetryOutcome(ctx context.Context, o llmretry.Outcome) {
	if s := retryOutcomeSinkFromContext(ctx); s != nil {
		s.Append(o)
	}
}
