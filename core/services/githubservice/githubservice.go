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
	ConnectRepo(ctx context.Context, userID, owner, repoName, accessToken string, botUserName string) (*store.GitHubRepo, error)
	ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error)
	ListRepos(ctx context.Context) ([]*store.GitHubRepo, error)
	DisconnectRepo(ctx context.Context, repoID string) error
	PR(ctx context.Context, repoID string, prNumber int) (*PullRequestDetails, error)
	ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error)
	PostComment(ctx context.Context, repoID string, prNumber int, comment string) error
}

type service struct {
	dbInstance libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
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

func (s *service) ListPRs(ctx context.Context, repoID string) ([]*PullRequest, error) {
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

func (s *service) ListRepos(ctx context.Context) ([]*store.GitHubRepo, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	return storeInstance.ListGitHubRepos(ctx)
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

func (s *service) ListComments(ctx context.Context, repoID string, prNumber int, since time.Time) ([]*github.IssueComment, error) {
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

func FormatSubjectID(repoID string, prNumber any) string {
	return fmt.Sprintf("%v-%v", repoID, prNumber)
}
