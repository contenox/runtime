package localfileapi

import (
	"context"

	"github.com/contenox/runtime/runtime/agentview"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/localfileservice"
)

type Entry struct {
	localfileservice.Entry
	Access *agentview.Verdict `json:"access,omitempty"`
}

type FileFilter interface {
	Name() string
	Apply(ctx context.Context, entries []Entry, ev *agentview.Evaluator) ([]Entry, error)
}

type PolicyEvaluatorFactory func(policyName string) hitlservice.Service

func defaultFilters() map[string]FileFilter {
	return map[string]FileFilter{
		agentFilter{}.Name(): agentFilter{},
	}
}

type agentFilter struct{}

func (agentFilter) Name() string { return "agent" }

func (agentFilter) Apply(ctx context.Context, entries []Entry, ev *agentview.Evaluator) ([]Entry, error) {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		v := ev.Verdict(ctx, e.Path, e.IsDirectory)
		e.Access = &v
		out[i] = e
	}
	return out, nil
}
