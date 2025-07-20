package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/google/uuid"
)

const (
	JobTypeGitHubCommentSync       = "github_comment_sync"
	JobTypeGitHubProcessCommentLLM = "github_process_comment_llm"
	DefaultLeaseDuration           = 30 * time.Second
)

type Worker interface {
	ReceiveTick(ctx context.Context) error
	ProcessTick(ctx context.Context) error
	serverops.ServiceMeta
}

type worker struct {
	githubService    Service
	kvManager        libkv.KVManager
	tracker          serverops.ActivityTracker
	dbInstance       libdb.DBManager
	bootLastSyncTime time.Time
}

func NewWorker(
	githubService Service,
	kvManager libkv.KVManager,
	tracker serverops.ActivityTracker,
	dbInstance libdb.DBManager,
	bootLastSyncTime time.Time,
) Worker {
	return &worker{
		githubService:    githubService,
		kvManager:        kvManager,
		tracker:          tracker,
		dbInstance:       dbInstance,
		bootLastSyncTime: bootLastSyncTime,
	}
}

func (w *worker) ReceiveTick(ctx context.Context) error {
	// Track receive tick
	reportErr, _, end := w.tracker.Start(ctx, "receive_tick", "github_comment_sync")
	defer end()

	storeInstance := store.New(w.dbInstance.WithoutTransaction())

	repos, err := w.githubService.ListRepos(ctx)
	if err != nil {
		reportErr(fmt.Errorf("failed to list repositories: %w", err))
		return err
	}

	jobs := []*store.Job{}
	for _, repo := range repos {
		prs, err := w.githubService.ListPRs(ctx, repo.ID)
		if err != nil {
			reportErr(fmt.Errorf("failed to list PRs for repo %s: %w", repo.ID, err))
			return fmt.Errorf("failed to list PRs for repo %s: %w", repo.ID, err)
		}

		for _, pr := range prs {
			job, err := w.createJobForPR(repo.ID, pr.Number)
			if err != nil {
				reportErr(fmt.Errorf("failed to create job for repo %s pr %d: %w", repo.ID, pr.Number, err))
				return fmt.Errorf("failed to create job for repo %s pr %d: %w", repo.ID, pr.Number, err)
			}
			jobs = append(jobs, job)
		}
	}

	if len(jobs) > 0 {
		if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
			reportErr(fmt.Errorf("failed to append jobs: %w", err))
			return fmt.Errorf("failed to append jobs: %w", err)
		}
	}

	return nil
}

func (w *worker) createJobForPR(repoID string, prNumber int) (*store.Job, error) {
	payload, err := json.Marshal(struct {
		RepoID   string `json:"repo_id"`
		PRNumber int    `json:"pr_number"`
	}{
		RepoID:   repoID,
		PRNumber: prNumber,
	})
	if err != nil {
		return nil, err
	}

	return &store.Job{
		ID:        uuid.NewString(),
		TaskType:  JobTypeGitHubCommentSync,
		CreatedAt: time.Now().UTC(),
		Operation: "sync_pr",
		Payload:   payload,
		Subject:   fmt.Sprintf("%s:%d", repoID, prNumber),
	}, nil
}

func (w *worker) ProcessTick(ctx context.Context) error {
	reportErr, _, end := w.tracker.Start(ctx, "process", "tick")
	defer end()
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	leaseID := uuid.NewString()

	leasedJob, err := storeInstance.PopJobForType(ctx, JobTypeGitHubCommentSync)
	if err != nil {
		reportErr(err)
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("pop job: %w", err)
	}

	return w.processLeasedJob(ctx, storeInstance, leasedJob, leaseID)
}

func (w *worker) processLeasedJob(
	ctx context.Context,
	storeInstance store.Store,
	leasedJob *store.Job,
	leaseID string,
) error {
	var processErr error
	reportJobErr, _, endJob := w.tracker.Start(ctx, "process", "leased_job", "job_id", leasedJob.ID)
	defer func() {
		if processErr != nil {
			reportJobErr(fmt.Errorf("worker error: %w", processErr))
		}
		endJob()
	}()

	leaseDuration := DefaultLeaseDuration
	if err := storeInstance.AppendLeasedJob(ctx, *leasedJob, leaseDuration, leaseID); err != nil {
		return fmt.Errorf("lease job: %w", err)
	}

	var payload struct {
		RepoID   string `json:"repo_id"`
		PRNumber int    `json:"pr_number"`
	}
	if err := json.Unmarshal(leasedJob.Payload, &payload); err != nil {
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	processErr = w.syncPRComments(ctx, payload.RepoID, payload.PRNumber) // TODO this is super fragile
	if processErr == nil {
		return storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
	}

	if leasedJob.RetryCount >= 30 {
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("job %s: max retries reached", leasedJob.ID)
	}

	leasedJob.RetryCount++
	if err := storeInstance.DeleteLeasedJob(ctx, leasedJob.ID); err != nil {
		return fmt.Errorf("delete leased job for requeue: %w", err)
	}

	if err := storeInstance.AppendJob(ctx, *leasedJob); err != nil {
		return fmt.Errorf("requeue job: %w", err)
	}

	return fmt.Errorf("process job failed: %w", processErr)
}

type GithubMessage struct {
	ID        int64
	UserName  string
	UserEmail string
	UserID    string
	PR        int
	RepoID    string
	Body      string
}

func (w *worker) syncPRComments(ctx context.Context, repoID string, prNumber int) error {
	// Track PR processing
	reportErr, reportChange, end := w.tracker.Start(
		ctx,
		"process_pr",
		"github_comment",
		"repo", repoID,
		"pr", prNumber,
	)
	defer end()

	kvOp, err := w.kvManager.Operation(ctx)
	if err != nil {
		reportErr(fmt.Errorf("failed to create KV operation: %w", err))
		return err
	}
	// TODO: cannot parse \"\\\"2025-07-20T10:27:55.159545896Z\\\"\" as \"2006\""
	// Get last sync time
	lastSyncKey := w.lastSyncKey(repoID, prNumber)
	lastSyncBytes, err := kvOp.Get(ctx, []byte(lastSyncKey))
	var lastSync time.Time
	// Handle empty value and errors
	if err != nil || len(lastSyncBytes) == 0 {
		// Use boot time if value is missing/empty
		lastSync = w.bootLastSyncTime
		b := []byte(lastSync.Format(time.RFC3339))
		if setErr := kvOp.Set(ctx, libkv.KeyValue{
			Key:   []byte(lastSyncKey),
			Value: b,
		}); setErr != nil {
			reportErr(fmt.Errorf("failed to set last sync time: %w", setErr))
			return setErr
		}
	} else {
		// Parse existing value
		var parseErr error
		if lastSync, parseErr = time.Parse(time.RFC3339, string(lastSyncBytes)); parseErr != nil {
			reportErr(fmt.Errorf("failed to parse last sync time: %w", parseErr))
			return parseErr
		}
	}

	// Fetch new comments
	comments, err := w.githubService.ListComments(ctx, repoID, prNumber, lastSync)
	if err != nil {
		err := fmt.Errorf("failed to list comments: %w", err)
		reportErr(err)
		return err
	}

	if len(comments) == 0 {
		// Track empty comment list
		_, reportChange, end := w.tracker.Start(ctx, "empty", "github_comments", "repo", repoID, "pr", prNumber)
		defer end()
		reportChange("no_new_comments", nil)
		return nil
	}

	// Store comments
	var storedCount int
	streamID := fmt.Sprintf("%v-%v", repoID, prNumber)
	tx, commit, release, err := w.dbInstance.WithTransaction(ctx)
	defer release()
	if err != nil {
		err := fmt.Errorf("failed to start transaction: %w", err)
		reportErr(err)
		return err
	}

	storeInstance := store.New(tx)
	idxs, err := storeInstance.ListMessageIndices(ctx, serverops.DefaultAdminUser)
	if err != nil {
		err := fmt.Errorf("failed to list message indices: %w", err)
		reportErr(err)
		return err
	}
	found := false
	for _, v := range idxs {
		if v == streamID {
			found = true
		}
	}
	if !found {
		user, err := storeInstance.GetUserByEmail(ctx, serverops.DefaultAdminUser)
		if err != nil {
			err := fmt.Errorf("SERVER BUG %w", err)
			reportErr(err)
			return err
		}
		err = storeInstance.CreateMessageIndex(ctx, streamID, user.ID)
		if err != nil {
			err := fmt.Errorf("failed to create message index: %w", err)
			reportErr(err)
			return err
		}
	}
	messagesFromStore, err := storeInstance.ListMessages(ctx, streamID)
	if err != nil {
		err := fmt.Errorf("failed to list messages: %w", err)
		reportErr(err)
		return err
	}
	messagesFromGithub := make([]store.Message, 0, len(messagesFromStore))
	for _, comment := range comments {
		messageID := ""
		if comment.ID == nil {
			reportErr(errors.New("comment ID is nil"))
			continue // There is no way in syncing without a ID
		}
		messageID = fmt.Sprintf("%v-%v", prNumber, comment.GetID())
		type Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		m := Message{
			Role: "user",
		}
		if user := comment.GetUser(); user != nil {
			m.Content = "[" + user.GetName() + "]" + comment.GetBody()
			if user.GetEmail() == serverops.DefaultAdminUser {
				m.Role = "system"
			}
		}

		createdAt := time.Now().UTC()
		if t := comment.CreatedAt.GetTime(); t != nil {
			createdAt = *t
		}
		payload, err := json.Marshal(m)
		if err != nil {
			err = fmt.Errorf("failed to marshal message payload: %w", err)
			reportErr(err)
			return err
		}
		message := store.Message{
			ID:      messageID,
			IDX:     streamID,
			AddedAt: createdAt,
			Payload: payload,
		}
		messagesFromGithub = append(messagesFromGithub,
			message,
		)
	}
	missingMessages := []*store.Message{}

	for _, m := range messagesFromGithub {
		found := false
		for _, m2 := range messagesFromStore {
			if m2.ID == m.ID {
				found = true
			}
		}
		if !found {
			missingMessages = append(missingMessages, &m)
		}
	}
	// Append new messages
	if len(missingMessages) > 0 {
		if err := storeInstance.AppendMessages(ctx, missingMessages...); err != nil {
			reportErr(err)
			return fmt.Errorf("failed to append messages: %w", err)
		}
	}

	// Create LLM jobs for each new comment
	for _, msg := range missingMessages {
		// Extract GitHub comment ID from message ID
		parts := strings.Split(msg.ID, "-")
		var commentID string
		if len(parts) > 1 {
			commentID = parts[1]
		} else {
			return fmt.Errorf("invalid message ID format")
		}

		// Parse the message payload
		var githubMsg GithubMessage
		if err := json.Unmarshal(msg.Payload, &githubMsg); err != nil {
			reportErr(fmt.Errorf("failed to parse message payload: %w", err))
			return fmt.Errorf("failed to parse message payload: %w", err)
		}

		// Create job payload
		payload, err := json.Marshal(struct {
			RepoID    string `json:"repo_id"`
			PRNumber  int    `json:"pr_number"`
			CommentID string `json:"comment_id"`
			MessageID string `json:"message_id"`
			UserName  string `json:"user_name"`
			Content   string `json:"content"`
		}{
			RepoID:    repoID,
			PRNumber:  prNumber,
			CommentID: commentID,
			MessageID: msg.ID,
			UserName:  githubMsg.UserName,
			Content:   githubMsg.Body,
		})
		if err != nil {
			reportErr(fmt.Errorf("failed to marshal job payload: %w", err))
			return fmt.Errorf("failed to marshal job payload: %w", err)
		}
		reportJobErr, _, endJob := w.tracker.Start(ctx, "append", "job", "type", JobTypeGitHubProcessCommentLLM)
		defer endJob()

		// Create and append job
		job := &store.Job{
			ID:        uuid.NewString(),
			TaskType:  JobTypeGitHubProcessCommentLLM,
			CreatedAt: time.Now().UTC(),
			Operation: "process_comment_llm",
			Payload:   payload,
			Subject:   fmt.Sprintf("%s:%d", repoID, prNumber),
		}

		if err := storeInstance.AppendJob(ctx, *job); err != nil {
			reportJobErr(fmt.Errorf("failed to append job: %w", err))
			return fmt.Errorf("failed to append job: %w", err)
		}
	}
	err = commit(ctx)
	if err != nil {
		reportErr(err)
		return err
	}
	// Update last sync time
	newSync := time.Now().UTC()
	syncData, err := json.Marshal(newSync)
	if err != nil {
		reportErr(fmt.Errorf("failed to marshal last sync time: %w", err))
		return fmt.Errorf("failed to marshal last sync time: %w", err)
	}
	if err := kvOp.Set(ctx, libkv.KeyValue{
		Key:   []byte(lastSyncKey),
		Value: syncData,
	}); err != nil {
		err = fmt.Errorf("failed to update last sync time: %w", err)
		reportErr(err)
		return fmt.Errorf("failed to update last sync time: %w", err)
	}
	// Report successful storage
	if storedCount > 0 {
		reportChange("storedCount", storedCount)
	}

	return nil
}

// func (w *worker) processGitHubProcessCommentLLMJob(
// 	ctx context.Context,
// 	storeInstance store.Store,
// 	leasedJob *store.Job,
// 	leaseID string,
// ) error {
// 	// Parse job payload
// 	var payload struct {
// 		RepoID    string `json:"repo_id"`
// 		PRNumber  int    `json:"pr_number"`
// 		CommentID string `json:"comment_id"`
// 		MessageID string `json:"message_id"`
// 		UserName  string `json:"user_name"`
// 		Content   string `json:"content"`
// 	}
// 	if err := json.Unmarshal(leasedJob.Payload, &payload); err != nil {
// 		return fmt.Errorf("unmarshal payload: %w", err)
// 	}

// 	// Load chat history for the PR
// 	streamID := fmt.Sprintf("%v-%v", payload.RepoID, payload.PRNumber)
// 	messagesFromStore, err := storeInstance.ListMessages(ctx, streamID)
// 	if err != nil {
// 		return fmt.Errorf("failed to load messages: %w", err)
// 	}

// 	// Convert to libmodelprovider.Message
// 	var chatHistory []libmodelprovider.Message
// 	for _, msg := range messagesFromStore {
// 		var parsedMsg libmodelprovider.Message
// 		if err := json.Unmarshal(msg.Payload, &parsedMsg); err != nil {
// 			return fmt.Errorf("failed to parse message content: %w", err)
// 		}
// 		chatHistory = append(chatHistory, parsedMsg)
// 	}

// 	// Build prompt template using the full conversation
// 	prompt := fmt.Sprintf("You are reviewing a GitHub PR. Here's the conversation:\n")
// 	for _, m := range chatHistory {
// 		prompt += fmt.Sprintf("[%s] %s\n", m.Role, m.Content)
// 	}
// 	prompt += "Please provide a thoughtful response."

// 	// Build or load chain definition
// 	chain, err := tasksrecipes.GetChainDefinition(ctx, storeInstance, tasksrecipes.StandardChatChainID)
// 	if err != nil {
// 		return fmt.Errorf("failed to get chain: %w", err)
// 	}

// 	// Update chain with dynamic parameters
// 	for i := range chain.Tasks {
// 		task := &chain.Tasks[i]
// 		if task.Type == taskengine.ModelExecution && task.ExecuteConfig != nil {
// 			task.ExecuteConfig.Models = []string{"ollama/llama3"} // Example model
// 		}
// 		if task.ID == "append_user_message" && task.Hook != nil {
// 			task.Hook.Args["subject_id"] = streamID
// 		}
// 	}

// 	// Execute chain (LLM processing will happen here in full implementation)
// 	_, _, err = w.env.ExecEnv(ctx, chain, prompt, taskengine.DataTypeString)
// 	if err != nil {
// 		return fmt.Errorf("chain execution failed: %w", err)
// 	}

// 	// At this point, the LLM has processed the message
// 	// The actual response handling (e.g., posting to GitHub) is not implemented here

// 	return storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
// }

// Key generation functions
func (w *worker) lastSyncKey(repoID string, prNumber int) string {
	return fmt.Sprintf("github:repo:%s:pr:%d:last_sync", repoID, prNumber)
}

// ServiceMeta implementation
func (w *worker) GetServiceName() string {
	return "githubservice"
}

func (w *worker) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
