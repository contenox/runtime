package githubclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/libs/libdb"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

var (
	ErrInvalidGitHubConfig = errors.New("invalid GitHub configuration")
	ErrGitHubAPIError      = errors.New("GitHub API error")
)

// Client defines the minimal GitHub API operations needed by our system
type Client interface {
	// Methods used by Worker
	ListRepos(ctx context.Context) ([]*store.GitHubRepo, error)
	ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error)
	ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error)
	GetPRDiff(ctx context.Context, repoID string, prNumber int) (string, error)
	// Methods used by Processor
	PostComment(ctx context.Context, repoID string, prNumber int, comment string) error
}

type client struct {
	dbInstance   libdb.DBManager
	githubClient *github.Client
}

func New(db libdb.DBManager, githubClient *github.Client) Client {
	return &client{dbInstance: db, githubClient: githubClient}
}

func (s *client) ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	repo, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("ListPRs: failed to get repo: %w", err)
	}

	client := s.createGitHubClient(ctx, repo.AccessToken)

	var allPRs []*github.PullRequest
	opt := &github.PullRequestListOptions{
		State: "open",
		ListOptions: github.ListOptions{
			Page:    1,
			PerPage: 100,
		},
	}
	maxpages := 10
	for {
		prs, resp, err := client.PullRequests.List(ctx, repo.Owner, repo.RepoName, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list PRs: %w, Response: %+v", err, resp)
		}
		allPRs = append(allPRs, prs...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		maxpages -= 1
		if maxpages < 0 {
			break
		}
	}

	result := make([]*PullRequest, len(allPRs))
	for i, pr := range allPRs {
		result[i] = &PullRequest{
			ID:          pr.GetID(),
			Number:      pr.GetNumber(),
			Title:       pr.GetTitle(),
			State:       pr.GetState(),
			URL:         pr.GetHTMLURL(),
			CreatedAt:   pr.GetCreatedAt().Time,
			UpdatedAt:   pr.GetUpdatedAt().Time,
			AuthorLogin: pr.GetUser().GetLogin(),
		}
	}
	return result, nil
}

func (s *client) ListRepos(ctx context.Context) ([]*store.GitHubRepo, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	return storeInstance.ListGitHubRepos(ctx)
}

func (s *client) PostComment(ctx context.Context, repoID string, prNumber int, comment string) error {
	// Validate repo first
	if exists, err := s.RepoExists(ctx, repoID); err != nil || !exists {
		return fmt.Errorf("repository %s does not exist or is not connected", repoID)
	}

	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	repoMeta, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return fmt.Errorf("PostComment: failed to get repo: %w", err)
	}

	client := s.createGitHubClient(ctx, repoMeta.AccessToken)

	_, _, err = client.Issues.CreateComment(
		ctx,
		repoMeta.Owner,
		repoMeta.RepoName,
		prNumber,
		&github.IssueComment{
			Body: &comment,
			CreatedAt: &github.Timestamp{
				Time: time.Now().UTC(),
			},
			UpdatedAt: &github.Timestamp{
				Time: time.Now().UTC(),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("%w: failed to post comment: %v", ErrGitHubAPIError, err)
	}
	return nil
}

func (s *client) ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	repoMeta, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("ListComments: failed to get repo: %w", err)
	}

	client := s.createGitHubClient(ctx, repoMeta.AccessToken)

	var allComments []*github.IssueComment
	opt := &github.IssueListCommentsOptions{
		Since:       &since,
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		comments, resp, err := client.Issues.ListComments(
			ctx,
			repoMeta.Owner,
			repoMeta.RepoName,
			prNumber,
			opt,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to list comments: %v", ErrGitHubAPIError, err)
		}
		allComments = append(allComments, comments...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return allComments, nil
}

func (s *client) createGitHubClient(ctx context.Context, accessToken string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: accessToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}

func (c *client) GetPRDiff(ctx context.Context, repoID string, prNumber int) (string, error) {
	// 1. Get repository metadata from store
	storeInstance := store.New(c.dbInstance.WithoutTransaction())
	repoMeta, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return "", fmt.Errorf("GetPRDiff: failed to get repo metadata: %w", err)
	}

	// 2. Create authenticated GitHub client
	client := c.createGitHubClient(ctx, repoMeta.AccessToken)

	// 3. Fetch raw diff in patch format
	diffBytes, _, err := client.PullRequests.GetRaw(
		ctx,
		repoMeta.Owner,
		repoMeta.RepoName,
		prNumber,
		github.RawOptions{
			Type: github.Diff,
		},
	)
	if err != nil {
		return "", fmt.Errorf("%w: failed to get PR diff: %v", ErrGitHubAPIError, err)
	}

	return string(diffBytes), nil
}

func (s *client) RepoExists(ctx context.Context, repoID string) (bool, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	_, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if errors.Is(err, libdb.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func FormatSubjectID(repoID string, prNumber any) string {
	return fmt.Sprintf("%v-%v", repoID, prNumber)
}

type PullRequest struct {
	ID          int64      `json:"id"`
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	URL         string     `json:"url"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	AuthorLogin string     `json:"authorLogin"`
	ClosedAt    *time.Time `json:"closedAt,omitempty"`
	MergedAt    *time.Time `json:"mergedAt,omitempty"`
}
