package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

type GitHubCommentProcessor struct {
	db  libdb.DBManager
	env taskengine.EnvExecutor
}

type jobPayload struct {
	RepoID    string `json:"repo_id"`
	PRNumber  int    `json:"pr_number"`
	CommentID string `json:"comment_id"`
	MessageID string `json:"message_id"`
	UserName  string `json:"user_name"`
	Content   string `json:"content"`
}

func NewGitHubCommentProcessor(db libdb.DBManager, env taskengine.EnvExecutor) *GitHubCommentProcessor {
	return &GitHubCommentProcessor{db: db, env: env}
}

func (p *GitHubCommentProcessor) ProcessJob(ctx context.Context, job *store.Job) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	storeInstance := store.New(p.db.WithoutTransaction())

	// Find the bot that handles this job type
	bots, err := storeInstance.ListBotsByJobType(ctx, job.TaskType)
	if err != nil {
		return fmt.Errorf("failed to find bot for job type: %w", err)
	}
	if len(bots) == 0 {
		return fmt.Errorf("no bot found for job type: %s", job.TaskType)
	}
	bot := bots[0]

	// Get the task chain
	chain, err := tasksrecipes.GetChainDefinition(ctx, p.db.WithoutTransaction(), bot.TaskChainID)
	if err != nil {
		return fmt.Errorf("failed to get task chain: %w", err)
	}

	// Configure chain with subject context
	subjectID := fmt.Sprintf("%s:%d", payload.RepoID, payload.PRNumber)
	for i := range chain.Tasks {
		task := &chain.Tasks[i]
		if task.Hook == nil {
			continue
		}
		switch task.ID {
		case "append_user_message", "persist_messages", "preappend_message_to_history":
			task.Hook.Args["subject_id"] = subjectID
		}
	}

	// Execute the chain
	result, _, err := p.env.ExecEnv(ctx, chain, payload.Content, taskengine.DataTypeString)
	if err != nil {
		return fmt.Errorf("failed to execute chain: %w", err)
	}

	// Extract the response
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		return errors.New("invalid chain result - expected chat history")
	}
	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		return fmt.Errorf("unexpected message role in response: %s", lastMsg.Role)
	}

	// Post the response to GitHub
	if err := p.postGitHubComment(ctx, payload.RepoID, payload.PRNumber, lastMsg.Content); err != nil {
		return fmt.Errorf("failed to post GitHub comment: %w", err)
	}

	// Store the assistant's message in the message history
	message := store.Message{
		ID:      fmt.Sprintf("response-%s", payload.MessageID),
		IDX:     subjectID,
		AddedAt: time.Now().UTC(),
		Payload: []byte(fmt.Sprintf(`{"role":"assistant","content":"%s"}`, lastMsg.Content)),
	}
	if err := storeInstance.AppendMessages(ctx, &message); err != nil {
		return fmt.Errorf("failed to store response message: %w", err)
	}

	return nil
}

func (p *GitHubCommentProcessor) postGitHubComment(ctx context.Context, repoID string, prNumber int, comment string) error {
	githubService := New(p.db) // Assuming githubservice is initialized with the same DB
	return githubService.PostComment(ctx, repoID, prNumber, comment)
}
