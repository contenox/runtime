package githubservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/githubclient"
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
	ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string, botUserName string) (*store.GitHubRepo, error)
	ListPRs(ctx context.Context, repoID string) ([]*githubclient.PullRequest, error)
	ListRepos(ctx context.Context) ([]*store.GitHubRepo, error)
	DisconnectRepo(ctx context.Context, repoID string) error
	PR(ctx context.Context, repoID string, prNumber int) (*PullRequestDetails, error)
	ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error)
	PostComment(ctx context.Context, repoID string, prNumber int, comment string) error
}

type service struct {
	dbInstance   libdb.DBManager
	githubClient githubclient.Client
}

func New(db libdb.DBManager, githubClient githubclient.Client) Service {
	return &service{dbInstance: db, githubClient: githubClient}
}

func (s *service) ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string, botUserName string) (*store.GitHubRepo, error) {
	if userID == "" || owner == "" || repoName == "" || accessToken == "" || botUserName == "" {
		return nil, ErrInvalidGitHubConfig
	}

	// Validate token and repository access
	client := s.createGitHubClient(ctx, accessToken)
	_, _, err := client.Repositories.Get(ctx, owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to verify repository access: %w", err)
	}

	repo := &store.GitHubRepo{
		ID:          fmt.Sprintf("%s-%s", owner, repoName),
		UserID:      userID,
		Owner:       owner,
		RepoName:    repoName,
		AccessToken: accessToken,
		BotUserName: botUserName,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	if err := storeInstance.CreateGitHubRepo(ctx, repo); err != nil {
		return nil, fmt.Errorf("failed to save repo: %w", err)
	}
	return repo, nil
}

func (s *service) ListPRs(ctx context.Context, repoID string) ([]*githubclient.PullRequest, error) {
	return s.githubClient.ListPRs(ctx, repoID)
}

func (s *service) ListRepos(ctx context.Context) ([]*store.GitHubRepo, error) {
	return s.githubClient.ListRepos(ctx)
}

func (s *service) DisconnectRepo(ctx context.Context, repoID string) error {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	return storeInstance.DeleteGitHubRepo(ctx, repoID)
}

type PullRequestDetails struct {
	PullRequest   *github.PullRequest
	ChangedFiles  []*github.CommitFile
	IssueComments []*github.IssueComment
	Reviews       []*github.PullRequestReview
}

func (s *service) PR(ctx context.Context, repoID string, prNumber int) (*PullRequestDetails, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	repoMeta, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	client := s.createGitHubClient(ctx, repoMeta.AccessToken)

	// Fetch PR details
	pr, _, err := client.PullRequests.Get(ctx, repoMeta.Owner, repoMeta.RepoName, prNumber)
	if err != nil {
		return nil, err
	}

	// Fetch changed files (with pagination)
	var allFiles []*github.CommitFile
	fileOpt := &github.ListOptions{PerPage: 100}
	maxPages := 10
	for {
		files, resp, err := client.PullRequests.ListFiles(ctx, repoMeta.Owner, repoMeta.RepoName, prNumber, fileOpt)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, files...)
		if resp.NextPage == 0 {
			break
		}
		fileOpt.Page = resp.NextPage
		maxPages -= 1
		if maxPages <= 0 {
			break
		}
	}
	maxPages = 10
	// Fetch general comments (with pagination)
	var allIssueComments []*github.IssueComment
	issueCommentOpt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := client.Issues.ListComments(ctx, repoMeta.Owner, repoMeta.RepoName, prNumber, issueCommentOpt)
		if err != nil {
			return nil, err
		}
		allIssueComments = append(allIssueComments, comments...)
		if resp.NextPage == 0 {
			break
		}
		issueCommentOpt.Page = resp.NextPage
		if maxPages <= 0 {
			break
		}
	}

	// Fetch reviews (with pagination)
	var allReviews []*github.PullRequestReview
	reviewOpt := &github.ListOptions{PerPage: 100}
	for {
		reviews, resp, err := client.PullRequests.ListReviews(ctx, repoMeta.Owner, repoMeta.RepoName, prNumber, reviewOpt)
		if err != nil {
			return nil, err
		}
		allReviews = append(allReviews, reviews...)
		if resp.NextPage == 0 {
			break
		}
		reviewOpt.Page = resp.NextPage
	}

	return &PullRequestDetails{
		PullRequest:   pr,
		ChangedFiles:  allFiles,
		IssueComments: allIssueComments,
		Reviews:       allReviews,
	}, nil
}

func (s *service) PostComment(ctx context.Context, repoID string, prNumber int, comment string) error {
	return s.githubClient.PostComment(ctx, repoID, prNumber, comment)
}

func (s *service) ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error) {

	return s.githubClient.ListComments(ctx, repoID, prNumber, since)
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

func (s *service) RepoExists(ctx context.Context, repoID string) (bool, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	_, err := storeInstance.GetGitHubRepo(ctx, repoID)
	if errors.Is(err, libdb.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
