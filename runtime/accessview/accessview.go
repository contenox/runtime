package accessview

import (
	"context"
	"os"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/vfs"
)

type DimensionVerdict struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	Rule   *int   `json:"rule,omitempty"`
}

type PathVerdict struct {
	Path      string            `json:"path"`
	Reachable bool              `json:"reachable"`
	Read      *DimensionVerdict `json:"read,omitempty"`
	Write     *DimensionVerdict `json:"write,omitempty"`
}

const (
	probeTools = "local_fs"
	probeTool  = "read_file"
)

type Evaluator struct {
	view *vfs.View
	hitl hitlservice.Service
}

func NewEvaluator(view *vfs.View, hitl hitlservice.Service) *Evaluator {
	return &Evaluator{view: view, hitl: hitl}
}

func (e *Evaluator) Evaluate(ctx context.Context, paths []string) (policyName string, verdicts []PathVerdict) {
	if e == nil || e.view == nil || e.hitl == nil {
		return "", nil
	}
	verdicts = make([]PathVerdict, len(paths))
	for i, p := range paths {
		v, resolved := e.verdict(ctx, p)
		verdicts[i] = v
		if policyName == "" && resolved != "" {
			policyName = resolved
		}
	}
	if policyName == "" {
		if res, err := e.hitl.Evaluate(ctx, probeTools, probeTool, map[string]any{"path": "."}); err == nil {
			policyName = res.PolicyName
		}
	}
	return policyName, verdicts
}

func (e *Evaluator) verdict(ctx context.Context, rootRelPath string) (PathVerdict, string) {
	pv := PathVerdict{Path: rootRelPath}

	abs, err := e.view.Resolve(rootRelPath)
	if err != nil {
		return pv, ""
	}
	pv.Reachable = true

	isDir := false
	if info, statErr := os.Stat(abs); statErr == nil {
		isDir = info.IsDir()
	}

	args := map[string]any{"path": rootRelPath}
	readTool := "read_file"
	if isDir {
		readTool = "list_dir"
	}

	var policyName string
	if res, err := e.hitl.Evaluate(ctx, "local_fs", readTool, args); err == nil {
		pv.Read = toDimensionVerdict(res)
		policyName = res.PolicyName
	}
	if res, err := e.hitl.Evaluate(ctx, "local_fs", "write_file", args); err == nil {
		pv.Write = toDimensionVerdict(res)
		policyName = res.PolicyName
	}
	return pv, policyName
}

func toDimensionVerdict(res hitlservice.EvaluationResult) *DimensionVerdict {
	dv := &DimensionVerdict{
		Action: string(res.Action),
		Reason: res.Reason,
	}
	if res.Reason == hitlservice.ReasonMatchedRule {
		dv.Rule = res.MatchedRule
	}
	return dv
}
