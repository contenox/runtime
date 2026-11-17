package autosetup

import (
	"fmt"
	"strings"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/runtime/modelregistry"
)

// Importance is how much a person values a role against the other roles sharing
// its memory pools. An absent importance plans the role as ImportanceNormal.
type Importance string

const (
	// ImportanceLow plans the role last and takes its smallest qualified candidate.
	ImportanceLow Importance = "low"
	// ImportanceNormal keeps the documented planning order and candidate choice.
	ImportanceNormal Importance = "normal"
	// ImportanceHigh plans the role first and takes its largest qualified candidate.
	ImportanceHigh Importance = "high"
)

// CandidateChoice selects which qualified candidate an importance level takes first.
type CandidateChoice string

const (
	// ChoiceLargest prefers the largest context, then the largest allocation.
	ChoiceLargest CandidateChoice = "largest"
	// ChoiceDocumented keeps the role's documented candidate order.
	ChoiceDocumented CandidateChoice = "documented"
	// ChoiceSmallest prefers the smallest context, then the smallest allocation.
	ChoiceSmallest CandidateChoice = "smallest"
)

// ImportanceRule is the planning data one importance level selects. Rank orders
// pool consumption, lowest first; MinChatContextTokens is the smallest context a
// Chat or Recovery candidate qualifies with; HeadroomFrac is the share of a
// worker's memory budget kept free above its estimated allocation.
type ImportanceRule struct {
	Importance           Importance      `json:"importance"`
	Rank                 int             `json:"rank"`
	Choice               CandidateChoice `json:"choice"`
	MinChatContextTokens int             `json:"minChatContextTokens"`
	HeadroomFrac         float64         `json:"headroomFrac"`
}

var importanceRules = []ImportanceRule{
	{Importance: ImportanceHigh, Rank: 0, Choice: ChoiceLargest, MinChatContextTokens: modelregistry.MinAgenticHotContext, HeadroomFrac: capacity.DefaultHeadroomFrac},
	{Importance: ImportanceNormal, Rank: 1, Choice: ChoiceDocumented, MinChatContextTokens: modelregistry.MinAgenticHotContext, HeadroomFrac: capacity.DefaultHeadroomFrac},
	{Importance: ImportanceLow, Rank: 2, Choice: ChoiceSmallest, MinChatContextTokens: modelregistry.MinAgenticHotContext, HeadroomFrac: capacity.DefaultHeadroomFrac},
}

// ImportanceRules returns the planning data of every importance level, highest first.
func ImportanceRules() []ImportanceRule {
	return append([]ImportanceRule(nil), importanceRules...)
}

// ImportanceIDs returns the declared importance vocabulary, highest first.
func ImportanceIDs() []string {
	ids := make([]string, 0, len(importanceRules))
	for _, rule := range importanceRules {
		ids = append(ids, string(rule.Importance))
	}
	return ids
}

func importanceRule(importance Importance) (ImportanceRule, error) {
	if importance == "" {
		importance = ImportanceNormal
	}
	for _, rule := range importanceRules {
		if rule.Importance == importance {
			return rule, nil
		}
	}
	return ImportanceRule{}, fmt.Errorf("unknown role importance %q: choose one of %s", importance, strings.Join(ImportanceIDs(), ", "))
}
