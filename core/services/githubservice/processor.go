package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

type GitHubCommentProcessor struct {
	db      libdb.DBManager
	env     taskengine.EnvExecutor
	tracker serverops.ActivityTracker
}

func NewGitHubCommentProcessor(db libdb.DBManager, env taskengine.EnvExecutor, tracker serverops.ActivityTracker) *GitHubCommentProcessor {
	if tracker == nil {
		tracker = serverops.NoopTracker{}
	}
	return &GitHubCommentProcessor{db: db, env: env, tracker: tracker}
}

func (p *GitHubCommentProcessor) ProcessJob(ctx context.Context, job *store.Job) (err error) {
	// Start activity tracking
	reportErr, reportChange, end := p.tracker.Start(
		ctx,
		"process",
		"github-job",
		"job_id", job.ID,
		"task_type", job.TaskType,
	)
	defer end()

	// Defer error reporting and state change
	var changeData map[string]interface{}
	defer func() {
		if err == nil && changeData != nil {
			reportChange(changeData["message_id"].(string), changeData)
		}
	}()

	// Unmarshal payload
	var payload GithubMessage
	if err = json.Unmarshal(job.Payload, &payload); err != nil {
		err = fmt.Errorf("failed to unmarshal job payload: %w", err)
		reportErr(err)
		return
	}

	storeInstance := store.New(p.db.WithoutTransaction())

	// Find bot for job type
	bots, err := storeInstance.ListBotsByJobType(ctx, job.TaskType)
	if err != nil {
		err = fmt.Errorf("failed to find bot for job type: %w", err)
		reportErr(err)
		return
	}
	if len(bots) == 0 {
		err = fmt.Errorf("no bot found for job type: %s", job.TaskType)
		reportErr(err)
		return
	}
	bot := bots[0]

	// Get task chain
	chain, err := tasksrecipes.GetChainDefinition(ctx, p.db.WithoutTransaction(), bot.TaskChainID)
	if err != nil {
		err = fmt.Errorf("failed to get task chain: %w", err)
		reportErr(err)
		return
	}

	// Configure chain with subject context
	subjectID := fmt.Sprintf("%s:%d", payload.RepoID, payload.PR)
	for i := range chain.Tasks {
		task := &chain.Tasks[i]
		if task.Hook == nil {
			continue
		}
		if task.Hook.Args == nil {
			task.Hook.Args = make(map[string]string)
		}
		task.Hook.Args["subject_id"] = subjectID
	}

	// Execute chain
	result, _, err := p.env.ExecEnv(ctx, chain, payload.Content, taskengine.DataTypeString)
	if err != nil {
		err = fmt.Errorf("failed to execute chain: %w", err)
		reportErr(err)
		return
	}

	// Extract response
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		err = errors.New("invalid chain result - expected chat history")
		reportErr(err)
		return
	}
	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		err = fmt.Errorf("unexpected message role in response: %s", lastMsg.Role)
		reportErr(err)
		return
	}

	// Post response to GitHub
	if err = p.postGitHubComment(ctx, payload.RepoID, payload.PR, lastMsg.Content); err != nil {
		err = fmt.Errorf("failed to post GitHub comment: %w", err)
		reportErr(err)
		return
	}

	// Store assistant message
	type chatMessage struct {
		Role    string
		Message string
	}
	jsonBytes, err := json.Marshal(chatMessage{
		Role:    lastMsg.Role,
		Message: lastMsg.Content,
	})
	if err != nil {
		err = fmt.Errorf("failed to marshal chat message: %w", err)
		reportErr(err)
		return
	}
	messageID := fmt.Sprintf("%v-%v", payload.PR, payload.CommentID)
	message := store.Message{
		ID:      fmt.Sprintf("response-%s", messageID),
		IDX:     subjectID,
		AddedAt: time.Now().UTC(),
		Payload: jsonBytes,
	}
	if err = storeInstance.AppendMessages(ctx, &message); err != nil {
		err = fmt.Errorf("failed to store response message: %w", err)
		reportErr(err)
		return
	}

	// Prepare success data
	changeData = map[string]interface{}{
		"repo_id":            payload.RepoID,
		"pr_number":          payload.PR,
		"comment_id":         payload.CommentID,
		"message_id":         payload.CommentID,
		"assistant_response": lastMsg.Content,
	}

	return nil
}

func (p *GitHubCommentProcessor) postGitHubComment(ctx context.Context, repoID string, prNumber int, comment string) error {
	githubService := New(p.db)
	return githubService.PostComment(ctx, repoID, prNumber, comment)
}
