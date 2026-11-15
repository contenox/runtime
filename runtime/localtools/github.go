package localtools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/workspacegithub"
)

type workspacePulls struct {
	service workspacegithub.Service
	cwd     func(context.Context) string
}

func WithWorkspaceGitHub(service workspacegithub.Service, cwd func(context.Context) string) GitOption {
	return func(g *GitTools) { g.github = &workspacePulls{service: service, cwd: cwd} }
}
func (g *workspacePulls) tools(ctx context.Context) []taskengine.Tool {
	if g == nil {
		return nil
	}
	b, err := g.service.Binding(ctx, g.cwd(ctx))
	if err != nil || b == nil {
		return nil
	}
	cursor := fsProp("string", "Continuation from the previous page")
	number := fsProp("integer", "Pull request number in the connected repository")
	return []taskengine.Tool{
		fsTool("github_pulls", "List open pull requests in this workspace's connected GitHub repository.", map[string]any{"cursor": cursor}),
		fsTool("github_pull", "Read a pull request, including its description, branches and exact head commit.", map[string]any{"number": number}, "number"),
		fsTool("github_files", "Read one page of changed files and patches in a pull request.", map[string]any{"number": number, "cursor": cursor}, "number"),
		fsTool("github_comments", "Read one page of inline review comments and their reply relationships.", map[string]any{"number": number, "cursor": cursor}, "number"),
	}
}
func (g *workspacePulls) read(ctx context.Context, name string, args map[string]any) (any, taskengine.DataType, error) {
	if g == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("GitHub tools unavailable")
	}
	allowed := []string{"number", "cursor"}
	if name == "github_pulls" {
		allowed = []string{"cursor"}
	} else if name == "github_pull" {
		allowed = []string{"number"}
	}
	if err := rejectUnknownArgs(name, args, allowed...); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	number, _ := strconv.Atoi(fmt.Sprint(args["number"]))
	cursor, _ := args["cursor"].(string)
	result, err := g.service.Read(ctx, g.cwd(ctx), 0, strings.TrimPrefix(name, "github_"), number, cursor)
	return result, taskengine.DataTypeAny, err
}
