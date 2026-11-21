package contenoxcli

import (
	"context"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/workflows"
)

func newWorkflowService(db libdb.DBManager, id string, engine *enginesvc.Engine, tracker libtracker.ActivityTracker, provider, model string, authorizeRead func(context.Context, string) error) *workflows.Service {
	s := &workflows.Service{DB: db, WorkspaceID: id, AuthorizeRead: authorizeRead}
	s.Model = func(ctx context.Context, prompt string) (string, workflows.Usage, error) {
		start := time.Now()
		usage := workflows.Usage{Provider: provider, Model: model}
		candidates, err := engine.State.ExecutionProviders(tracker)(ctx, provider)
		if err != nil {
			return "", usage, err
		}
		for _, p := range candidates {
			if p.ModelName() != model || !p.CanChat() {
				continue
			}
			ids := p.GetBackendIDs()
			if len(ids) == 0 {
				return "", usage, fmt.Errorf("no backend for %s", model)
			}
			client, e := p.GetChatConnection(ctx, ids[0])
			if e != nil {
				return "", usage, e
			}
			answer, e := client.Chat(ctx, []modelrepo.Message{{Role: "system", Content: "Complete the workflow request using the supplied data. Return only the requested answer."}, {Role: "user", Content: prompt}}, modelrepo.WithMaxTokens(2048))
			usage.Milliseconds = time.Since(start).Milliseconds()
			if u := answer.Usage; u != nil {
				usage.Input = u.PromptTokens
				usage.Output = u.CompletionTokens
			}
			return answer.Message.Content, usage, e
		}
		return "", usage, fmt.Errorf("configured model %s on %s is unavailable", model, provider)
	}
	return s
}
