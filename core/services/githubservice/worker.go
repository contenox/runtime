package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/google/uuid"
)

const (
	JobTypeGitHubCommentSync = "github_comment_sync"
	DefaultLeaseDuration     = 30 * time.Second
)

type Worker interface {
	ReceiveTick(ctx context.Context) error
	ProcessTick(ctx context.Context) error
	serverops.ServiceMeta
}

type worker struct {
	githubService Service
	kvManager     libkv.KVManager
	tracker       serverops.ActivityTracker
	dbInstance    libdb.DBManager
}

func NewWorker(
	githubService Service,
	kvManager libkv.KVManager,
	tracker serverops.ActivityTracker,
	dbInstance libdb.DBManager,
) Worker {
	return &worker{
		githubService: githubService,
		kvManager:     kvManager,
		tracker:       tracker,
		dbInstance:    dbInstance,
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
			continue
		}

		for _, pr := range prs {
			job, err := w.createJobForPR(repo.ID, pr.Number)
			if err != nil {
				reportErr(fmt.Errorf("failed to create job for repo %s pr %d: %w", repo.ID, pr.Number, err))
				continue
			}
			jobs = append(jobs, job)
		}
	}

	if len(jobs) > 0 {
		if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
			reportErr(fmt.Errorf("failed to append jobs: %w", err))
			return err
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
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	leaseID := uuid.NewString()

	leasedJob, err := storeInstance.PopJobForType(ctx, JobTypeGitHubCommentSync)
	if err != nil {
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

	processErr := w.syncPRComments(ctx, payload.RepoID, payload.PRNumber)

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

	// Get last sync time
	lastSyncKey := w.lastSyncKey(repoID, prNumber)
	lastSyncBytes, err := kvOp.Get(ctx, []byte(lastSyncKey))

	var lastSync time.Time
	if errors.Is(err, libkv.ErrNotFound) {
		lastSync = time.Now().Add(-24 * time.Hour)
	} else if err != nil {
		err = fmt.Errorf("failed to get last sync time: %w", err)
		reportErr(err)
		return err
	} else if err := json.Unmarshal(lastSyncBytes, &lastSync); err != nil {
		lastSync = time.Now().Add(-24 * time.Hour)
	}

	// Fetch new comments
	comments, err := w.githubService.ListComments(ctx, repoID, prNumber, lastSync)
	if err != nil {
		err = fmt.Errorf("failed to list comments: %w", err)
		reportErr(err)
		return err
	}

	if len(comments) == 0 {
		return nil
	}

	// Store comments
	var storedCount int
	streamID := fmt.Sprintf("%v-%v", repoID, prNumber)
	tx, commit, release, err := w.dbInstance.WithTransaction(ctx)
	defer release()
	if err != nil {
		reportErr(err)
		return err
	}

	storeInstance := store.New(tx)
	idxs, err := storeInstance.ListMessageIndices(ctx, serverops.DefaultAdminUser)
	if err != nil {
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
			reportErr(err)
			return err
		}
	}
	messagesFromStore, err := storeInstance.ListMessages(ctx, streamID)
	if err != nil {
		reportErr(err)
		return err
	}
	messagesFromGithub := make([]store.Message, 0, len(messagesFromStore))
	for _, comment := range comments {
		messageID := ""
		if comment.ID == nil {
			continue // There is no way in syncing without a ID
		}
		messageID = fmt.Sprintf("%v-%v", prNumber, *comment.ID)
		m := GithubMessage{
			ID:     comment.GetID(),
			Body:   comment.GetBody(),
			RepoID: repoID,
			PR:     prNumber,
		}
		if user := comment.GetUser(); user != nil {
			m.UserName = user.GetName()
			m.UserEmail = user.GetEmail()
		}

		createdAt := time.Now().UTC()
		if t := comment.CreatedAt.GetTime(); t != nil {
			createdAt = *t
		}
		payload, err := json.Marshal(m)
		if err != nil {
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
	storeInstance.AppendMessages(ctx, missingMessages...)
	err = commit(ctx)
	if err != nil {
		reportErr(err)
		return err
	}
	// Update last sync time
	newSync := time.Now()
	syncData, _ := json.Marshal(newSync)
	if err := kvOp.Set(ctx, libkv.KeyValue{
		Key:   []byte(lastSyncKey),
		Value: syncData,
	}); err != nil {
		err = fmt.Errorf("failed to update last sync time: %w", err)
		reportErr(err)

	}

	// Report successful storage
	if storedCount > 0 {
		reportChange("", storedCount)
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
