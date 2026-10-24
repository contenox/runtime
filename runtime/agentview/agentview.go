package agentview

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/vfs"
)

type Op string

const (
	OpRead  Op = "read"
	OpWrite Op = "write"
)

type Verdict struct {
	Reachable   bool               `json:"reachable"`
	Read        hitlservice.Action `json:"read,omitempty"`
	Write       hitlservice.Action `json:"write,omitempty"`
	ReadReason  string             `json:"readReason,omitempty"`
	WriteReason string             `json:"writeReason,omitempty"`
}

type Evaluator struct {
	view       *vfs.View
	hitl       hitlservice.Service
	policyName string
}

func NewEvaluator(view *vfs.View, hitl hitlservice.Service, policyName string) *Evaluator {
	return &Evaluator{view: view, hitl: hitl, policyName: policyName}
}

func (e *Evaluator) PolicyName() string {
	if e == nil {
		return ""
	}
	return e.policyName
}

func (e *Evaluator) Verdict(ctx context.Context, rootRelPath string, isDir bool) Verdict {
	if e == nil || e.view == nil || e.hitl == nil {
		return Verdict{}
	}

	if _, err := e.view.Resolve(rootRelPath); err != nil {
		return Verdict{Reachable: false}
	}

	v := Verdict{Reachable: true}
	args := map[string]any{"path": rootRelPath}

	readTool := "read_file"
	if isDir {
		readTool = "list_dir"
	}
	if res, err := e.hitl.Evaluate(ctx, "local_fs", readTool, args); err == nil {
		v.Read = res.Action
		v.ReadReason = interestingReason(res)
	}
	if res, err := e.hitl.Evaluate(ctx, "local_fs", "write_file", args); err == nil {
		v.Write = res.Action
		v.WriteReason = interestingReason(res)
	}
	return v
}

func interestingReason(res hitlservice.EvaluationResult) string {
	if res.Action == hitlservice.ActionAllow {
		return ""
	}
	if res.MatchedRule != nil {
		return fmt.Sprintf("matched rule %d", *res.MatchedRule)
	}
	return "default action"
}
