package hitlservice

import (
	"context"
	"fmt"
	"strings"
)

type AttentionBounds struct {
	AllowAgentAnswers   bool `json:"allowAgentAnswers,omitempty"`
	MaxAgentAnswers     int  `json:"maxAgentAnswers,omitempty"`
	AllowAgentApprovals bool `json:"allowAgentApprovals,omitempty"`
	MaxAgentApprovals   int  `json:"maxAgentApprovals,omitempty"`
}

const DefaultMaxAgentAnswers = 3

const DefaultMaxAgentApprovals = 10

const maxAgentAnswersCeiling = 1_000

func (b AttentionBounds) EffectiveMaxAgentAnswers() int {
	if b.MaxAgentAnswers <= 0 {
		return DefaultMaxAgentAnswers
	}
	return b.MaxAgentAnswers
}

func (b AttentionBounds) EffectiveMaxAgentApprovals() int {
	if b.MaxAgentApprovals <= 0 {
		return DefaultMaxAgentApprovals
	}
	return b.MaxAgentApprovals
}

func (s *service) AttentionBoundsFor(ctx context.Context, policyName string) (AttentionBounds, error) {
	policyPath := strings.TrimSpace(policyName)
	if policyPath == "" {
		policyPath = s.fallbackPolicy
	}
	if policyPath == "" {
		policyPath = defaultPolicyName
	}
	p, err := loadPolicy(ctx, s.src, s.tenantID, policyPath)
	if err != nil {
		return AttentionBounds{}, fmt.Errorf("hitlservice: load attention bounds for %q: %w", policyPath, err)
	}
	if p.Attention == nil {
		return AttentionBounds{}, nil
	}
	return *p.Attention, nil
}

func validateAttentionBounds(b *AttentionBounds) error {
	if b == nil {
		return nil
	}
	if b.MaxAgentAnswers < 0 {
		return fmt.Errorf("attention.maxAgentAnswers must not be negative")
	}
	if b.MaxAgentAnswers > maxAgentAnswersCeiling {
		return fmt.Errorf("attention.maxAgentAnswers %d exceeds the sanity ceiling %d", b.MaxAgentAnswers, maxAgentAnswersCeiling)
	}
	if b.MaxAgentApprovals < 0 {
		return fmt.Errorf("attention.maxAgentApprovals must not be negative")
	}
	if b.MaxAgentApprovals > maxAgentAnswersCeiling {
		return fmt.Errorf("attention.maxAgentApprovals %d exceeds the sanity ceiling %d", b.MaxAgentApprovals, maxAgentAnswersCeiling)
	}
	return nil
}

var _ = context.Background
