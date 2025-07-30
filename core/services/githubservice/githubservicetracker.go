package githubservice

import (
	"context"
	"time"

	"github.com/contenox/activitytracker"
	"github.com/contenox/runtime-mvp/core/githubclient"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/google/go-github/v58/github"
)

type activityTrackerDecorator struct {
	service Service
	tracker activitytracker.ActivityTracker
}

// New decorator factory
func WithActivityTracker(service Service, tracker activitytracker.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}

// Implement Service interface methods with tracking
func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func (d *activityTrackerDecorator) ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string, botUserName string) (*store.GitHubRepo, error) {
	reportErr, reportChange, end := d.tracker.Start(
		ctx, "connect", "github-repo",
		"user_id", userID,
		"owner", owner,
		"repo", repoName,
		"bot_user_name", botUserName,
	)
	defer end()

	repo, err := d.service.ConnectRepo(ctx, userID, owner, repoName, accessToken, botUserName)
	if err != nil {
		reportErr(err)
		return nil, err
	}

	reportChange(repo.ID, map[string]interface{}{
		"repo_id":    repo.ID,
		"owner":      repo.Owner,
		"repo_name":  repo.RepoName,
		"created_at": repo.CreatedAt.Format(time.RFC3339),
		"updated_at": repo.UpdatedAt.Format(time.RFC3339),
	})

	return repo, nil
}

func (d *activityTrackerDecorator) ListPRs(ctx context.Context, repoID string) ([]*githubclient.PullRequest, error) {
	reportErr, _, end := d.tracker.Start(
		ctx, "list", "github-prs",
		"repo_id", repoID,
	)
	defer end()

	prs, err := d.service.ListPRs(ctx, repoID)
	if err != nil {
		reportErr(err)
		return nil, err
	}

	return prs, nil
}

func (d *activityTrackerDecorator) ListRepos(ctx context.Context) ([]*store.GitHubRepo, error) {
	reportErr, _, end := d.tracker.Start(ctx, "list", "github-repos")
	defer end()

	repos, err := d.service.ListRepos(ctx)
	if err != nil {
		reportErr(err)
		return nil, err
	}

	return repos, nil
}

func (d *activityTrackerDecorator) DisconnectRepo(ctx context.Context, repoID string) error {
	reportErr, reportChange, end := d.tracker.Start(
		ctx, "disconnect", "github-repo",
		"repo_id", repoID,
	)
	defer end()

	err := d.service.DisconnectRepo(ctx, repoID)
	if err != nil {
		reportErr(err)
		return err
	}

	reportChange(repoID, map[string]interface{}{
		"repo_id":   repoID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

func (d *activityTrackerDecorator) PR(ctx context.Context, repoID string, prNumber int) (*PullRequestDetails, error) {
	reportErr, _, end := d.tracker.Start(
		ctx, "get", "github-pr",
		"repo_id", repoID,
		"pr_number", prNumber,
	)
	defer end()

	details, err := d.service.PR(ctx, repoID, prNumber)
	if err != nil {
		reportErr(err)
		return nil, err
	}

	return details, nil
}

func (d *activityTrackerDecorator) ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error) {
	reportErr, _, end := d.tracker.Start(
		ctx, "list", "github-comments",
		"repo_id", repoID,
		"pr_number", prNumber,
		"since", since.Format(time.RFC3339),
	)
	defer end()

	comments, err := d.service.ListComments(ctx, repoID, prNumber, since)
	if err != nil {
		reportErr(err)
		return nil, err
	}

	return comments, nil
}

func (d *activityTrackerDecorator) PostComment(ctx context.Context, repoID string, prNumber int, comment string) error {
	reportErr, reportChange, end := d.tracker.Start(
		ctx, "post", "github-comment",
		"repo_id", repoID,
		"pr_number", prNumber,
		"comment_length", len(comment),
	)
	defer end()

	err := d.service.PostComment(ctx, repoID, prNumber, comment)
	if err != nil {
		reportErr(err)
		return err
	}

	reportChange(repoID, map[string]interface{}{
		"repo_id":   repoID,
		"pr_number": prNumber,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

// Helper functions

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
