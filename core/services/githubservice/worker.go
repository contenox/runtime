package githubservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/google/go-github/v58/github"
)

type Worker struct {
	githubService Service
	kvManager     libkv.KVManager
	logger        *slog.Logger
	interval      time.Duration
}

func NewWorker(
	githubService Service,
	kvManager libkv.KVManager,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		githubService: githubService,
		kvManager:     kvManager,
		logger:        logger,
		interval:      time.Hour,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("GitHub comment worker starting")

	// Run immediately on startup
	w.run(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("GitHub comment worker stopping")
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	startTime := time.Now()
	w.logger.Info("GitHub comment sync started")

	repos, err := w.githubService.ListRepos(ctx)
	if err != nil {
		w.logger.Error("Failed to list repositories", "error", err)
		return
	}

	for _, repo := range repos {
		prs, err := w.githubService.ListPRs(ctx, repo.ID)
		if err != nil {
			w.logger.Error("Failed to list PRs", "repo", repo.ID, "error", err)
			continue
		}

		for _, pr := range prs {
			if err := w.processPR(ctx, repo.ID, pr.Number); err != nil {
				w.logger.Error("Failed to process PR",
					"repo", repo.ID,
					"pr", pr.Number,
					"error", err,
				)
			}
		}
	}

	w.logger.Info("GitHub comment sync completed",
		"duration", time.Since(startTime),
	)
}

func (w *Worker) processPR(ctx context.Context, repoID string, prNumber int) error {
	kvOp, err := w.kvManager.Operation(ctx)
	if err != nil {
		return fmt.Errorf("failed to create KV operation: %w", err)
	}

	// Get last sync time
	lastSyncKey := w.lastSyncKey(repoID, prNumber)
	lastSyncBytes, err := kvOp.Get(ctx, []byte(lastSyncKey))

	var lastSync time.Time
	if errors.Is(err, libkv.ErrNotFound) {
		lastSync = time.Now().Add(-24 * time.Hour) // Default to 24 hours ago
	} else if err != nil {
		return fmt.Errorf("failed to get last sync time: %w", err)
	} else {
		if err := json.Unmarshal(lastSyncBytes, &lastSync); err != nil {
			lastSync = time.Now().Add(-24 * time.Hour)
		}
	}

	// Fetch new comments
	comments, err := w.githubService.ListComments(ctx, repoID, prNumber, lastSync)
	if err != nil {
		return fmt.Errorf("failed to list comments: %w", err)
	}

	if len(comments) == 0 {
		return nil
	}

	// Store comments
	for _, comment := range comments {
		if err := w.storeComment(ctx, kvOp, repoID, prNumber, comment); err != nil {
			w.logger.Error("Failed to store comment",
				"repo", repoID,
				"pr", prNumber,
				"commentID", comment.GetID(),
				"error", err,
			)
		}
	}

	// Update last sync time
	newSync := time.Now()
	syncData, _ := json.Marshal(newSync)
	if err := kvOp.Set(ctx, libkv.KeyValue{
		Key:   []byte(lastSyncKey),
		Value: syncData,
	}); err != nil {
		return fmt.Errorf("failed to update last sync time: %w", err)
	}

	w.logger.Info("Stored new comments",
		"repo", repoID,
		"pr", prNumber,
		"count", len(comments),
	)

	return nil
}

func (w *Worker) storeComment(
	ctx context.Context,
	kvOp libkv.KVExec,
	repoID string,
	prNumber int,
	comment *github.IssueComment,
) error {
	commentKey := w.commentKey(repoID, prNumber, comment.GetID())
	commentData, err := json.Marshal(comment)
	if err != nil {
		return fmt.Errorf("failed to marshal comment: %w", err)
	}

	// Store comment
	if err := kvOp.Set(ctx, libkv.KeyValue{
		Key:   []byte(commentKey),
		Value: commentData,
	}); err != nil {
		return fmt.Errorf("failed to store comment: %w", err)
	}

	// Add to comment index
	indexKey := w.commentIndexKey(repoID, prNumber)
	return kvOp.SAdd(ctx, []byte(indexKey), []byte(commentKey))
}

func (w *Worker) lastSyncKey(repoID string, prNumber int) string {
	return fmt.Sprintf("github:repo:%s:pr:%d:last_sync", repoID, prNumber)
}

func (w *Worker) commentKey(repoID string, prNumber int, commentID int64) string {
	return fmt.Sprintf("github:repo:%s:pr:%d:comment:%d", repoID, prNumber, commentID)
}

func (w *Worker) commentIndexKey(repoID string, prNumber int) string {
	return fmt.Sprintf("github:repo:%s:pr:%d:comments", repoID, prNumber)
}
