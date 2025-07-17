package githubservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

var (
	ErrInvalidGitHubConfig = errors.New("invalid GitHub configuration")
	ErrGitHubAPIError      = errors.New("GitHub API error")
)

type Service interface {
	serverops.ServiceMeta
	ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string) (*store.GitHubRepo, error)
	ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error)
	ListRepos(ctx context.Context) ([]*store.GitHubRepo, error)
	DisconnectRepo(ctx context.Context, repoID string) error
}

type service struct {
	dbInstance libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

func (s *service) ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string) (*store.GitHubRepo, error) {
	if userID == "" || owner == "" || repoName == "" || accessToken == "" {
		return nil, ErrInvalidGitHubConfig
	}

	// Validate token and repository access
	client := s.createGitHubClient(ctx, accessToken)
	_, _, err := client.Repositories.Get(ctx, owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to verify repository access: %w", err)
	}

	repo := &store.GitHubRepo{
		ID:          fmt.Sprintf("%s/%s", owner, repoName),
		UserID:      userID,
		Owner:       owner,
		RepoName:    repoName,
		AccessToken: accessToken,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	if err := storeInstance.CreateGitHubRepo(ctx, repo); err != nil {
		return nil, fmt.Errorf("failed to save repo: %w", err)
	}
	return repo, nil
}

func (s *service) ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	repo, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("repo not found: %w", err)
	}

	client := s.createGitHubClient(ctx, repo.AccessToken)
	prs, _, err := client.PullRequests.List(ctx, repo.Owner, repo.RepoName, &github.PullRequestListOptions{
		State: "open",
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGitHubAPIError, err)
	}

	result := make([]*PullRequest, len(prs))
	for i, pr := range prs {
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

func (s *service) ListRepos(ctx context.Context) ([]*store.GitHubRepo, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	return storeInstance.ListGitHubRepos(ctx)
}

func (s *service) DisconnectRepo(ctx context.Context, repoID string) error {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	return storeInstance.DeleteGitHubRepo(ctx, repoID)
}

func (s *service) createGitHubClient(ctx context.Context, accessToken string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: accessToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}

func (s *service) GetServiceName() string  { return "githubservice" }
func (s *service) GetServiceGroup() string { return serverops.DefaultDefaultServiceGroup }

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
