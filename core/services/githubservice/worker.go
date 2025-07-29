package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/githubclient"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/chatservice"
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
	githubClient     githubclient.Client
	kvManager        libkv.KVManager
	tracker          serverops.ActivityTracker
	dbInstance       libdb.DBManager
	bootLastSyncTime time.Time
}

func NewWorker(
	githubClient githubclient.Client,
	kvManager libkv.KVManager,
	tracker serverops.ActivityTracker,
	dbInstance libdb.DBManager,
	bootLastSyncTime time.Time,
) Worker {
	return &worker{
		githubClient:     githubClient,
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

	repos, err := w.githubClient.ListRepos(ctx)
	if err != nil {
		reportErr(fmt.Errorf("failed to list repositories: %w", err))
		return err
	}

	jobs := []*store.Job{}
	for _, repo := range repos {
		prs, err := w.githubClient.ListPRs(ctx, repo.ID)
		if err != nil {
			reportErr(fmt.Errorf("failed to list PRs for repo %s: %w", repo.ID, err))
			return fmt.Errorf("failed to list PRs for repo %s: %w", repo.ID, err)
		}

		for _, pr := range prs {
			job, err := w.createJobForPR(repo.ID, repo.BotUserName, pr.Number)
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

func (w *worker) createJobForPR(repoID string, botID string, prNumber int) (*store.Job, error) {
	var payload processLeasedJobPayload
	payload.RepoID = repoID
	payload.PRNumber = prNumber
	payload.BotID = botID

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &store.Job{
		ID:        uuid.NewString(),
		TaskType:  JobTypeGitHubCommentSync,
		CreatedAt: time.Now().UTC(),
		Operation: "sync_pr",
		Payload:   payloadBytes,
		Subject:   githubclient.FormatSubjectID(repoID, prNumber),
	}, nil
}

type lastSync struct {
	Time time.Time `json:"time"`
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

type processLeasedJobPayload struct {
	RepoID   string `json:"repoId"`
	BotID    string `json:"botId"`
	PRNumber int    `json:"prNumber"`
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
	var payload processLeasedJobPayload
	if err := json.Unmarshal(leasedJob.Payload, &payload); err != nil {
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	var lsync lastSync
	err := storeInstance.GetKV(ctx, w.lastSyncKey(payload.RepoID, payload.PRNumber), &lsync)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("get last sync: %w", err)
	}
	if w.bootLastSyncTime.After(lsync.Time) {
		lsync.Time = w.bootLastSyncTime
	}
	processErr = w.syncPRComments(ctx, payload.BotID, payload.RepoID, payload.PRNumber, lsync.Time)
	if processErr == nil {
		updateSyncTime := lastSync{
			Time: time.Now().UTC(),
		}
		b, err := json.Marshal(updateSyncTime)
		if err != nil {
			return fmt.Errorf("marshal last sync time: %w", err)
		}
		storeInstance.SetKV(ctx, w.lastSyncKey(payload.RepoID, payload.PRNumber), b)
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
	CommentID string `json:"commentID"`
	UserName  string `json:"userName"`
	UserEmail string `json:"userEmail"`
	UserID    string `json:"userID"`
	PR        int    `json:"pr"`
	RepoID    string `json:"repoID"`
	Content   string `json:"content"`
}

//	 // TODO add type and fix missing role.
//			{
//	    "role": "",
//	    "content": "this is a test message to ping the bot",
//	    "sentAt": "2025-07-27T08:47:01Z",
//	    "isUser": false,
//	    "isLatest": false
//	}
//
// Store message with full context
type MessagePayload struct {
	// "repo_id":    repoID,
	// "pr_number":  prNumber,
	// "comment_id": commentID,
	// "user_name":  userName,
	// "content":    content,
	// "sent_at":    sendAt,
	RepoID    string `json:"repoID"`
	PRNumber  int    `json:"prNumber"`
	CommentID string `json:"commentID"`
	UserName  string `json:"userName"`
	chatservice.ChatMessage
}

func (w *worker) syncPRComments(ctx context.Context, botID string, repoID string, prNumber int, lastSync time.Time) error {
	// Track PR processing
	reportErr, reportChange, end := w.tracker.Start(
		ctx,
		"process_pr",
		"github_comment",
		"repo", repoID,
		"pr", prNumber,
	)
	defer end()

	// Fetch new comments
	comments, err := w.githubClient.ListComments(ctx, repoID, prNumber, lastSync)
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
	streamID := githubclient.FormatSubjectID(repoID, prNumber)
	tx, commit, release, err := w.dbInstance.WithTransaction(ctx)
	defer release()
	if err != nil {
		err := fmt.Errorf("failed to start transaction: %w", err)
		reportErr(err)
		return err
	}

	storeInstance := store.New(tx)
	user, err := storeInstance.GetUserByEmail(ctx, serverops.DefaultAdminUser)
	if err != nil {
		err := fmt.Errorf("SERVER BUG %w", err)
		reportErr(err)
		return err
	}
	idxs, err := storeInstance.ListMessageIndices(ctx, user.ID)
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
	isUser := map[string]bool{}
	for _, comment := range comments {
		// Parse comment data
		commentID := fmt.Sprint(comment.GetID())
		if commentID == "0" {
			reportErr(errors.New("invalid comment ID"))
			continue
		}

		// Generate deterministic message ID
		messageID := fmt.Sprintf("gh-%d-%s", prNumber, commentID)

		// Create message content
		user := comment.GetUser()
		userName := "unknown"
		if user != nil {
			userName = user.GetLogin()
			if userName == "" {
				userName = user.GetName()
			}
			if userName == "" {
				userName = user.GetEmail()
			}
		}

		content := comment.GetBody()
		if content == "" {
			reportErr(errors.New("empty comment content"))
			continue
		}
		content = removeFooter(content)
		sendAt := time.Now().UTC()
		if !comment.GetCreatedAt().IsZero() {
			sendAt = comment.GetCreatedAt().Time
		}
		messageData := MessagePayload{
			RepoID:    repoID,
			PRNumber:  prNumber,
			CommentID: commentID,
			UserName:  userName,
			ChatMessage: chatservice.ChatMessage{
				ID:      messageID,
				Role:    "user",
				Content: content,
				SentAt:  sendAt,
				IsUser:  true,
			},
		}
		if botID == userName {
			messageData.ChatMessage.Role = "assistant"
			messageData.ChatMessage.IsUser = false
		}
		isUser[messageID] = messageData.ChatMessage.IsUser

		payload, err := json.Marshal(messageData)
		if err != nil {
			reportErr(fmt.Errorf("failed to marshal message: %w", err))
			continue
		}

		message := store.Message{
			ID:      messageID,
			IDX:     streamID,
			AddedAt: sendAt,
			Payload: payload,
		}
		messagesFromGithub = append(messagesFromGithub, message)
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
		if !isUser[msg.ID] {
			continue
		}
		job := &store.Job{
			ID:        uuid.NewString(),
			TaskType:  JobTypeGitHubProcessCommentLLM,
			CreatedAt: time.Now().UTC(),
			Operation: "process_comment_llm",
			Payload:   msg.Payload,
			Subject:   githubclient.FormatSubjectID(repoID, prNumber),
		}

		if err := storeInstance.AppendJob(ctx, *job); err != nil {
			reportErr(fmt.Errorf("failed to append job: %w", err))
		}
	}
	err = commit(ctx)
	if err != nil {
		reportErr(err)
		return err
	}
	// Report successful storage
	if storedCount > 0 {
		reportChange("storedCount", storedCount)
	}

	return nil
}

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
