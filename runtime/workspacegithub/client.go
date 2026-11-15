package workspacegithub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v58/github"
)

type PullRequest struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	AuthorLogin string `json:"authorLogin"`
	Head        string `json:"head"`
	HeadOwner   string `json:"headOwner"`
	Ref         string `json:"ref"`
	Base        string `json:"base"`
	URL         string `json:"url"`
	Draft       bool   `json:"draft"`
}

func pullRequest(pr *github.PullRequest) PullRequest {
	return PullRequest{Number: pr.GetNumber(), Title: pr.GetTitle(), Body: pr.GetBody(), AuthorLogin: pr.GetUser().GetLogin(), Head: pr.GetHead().GetRef(), HeadOwner: pr.GetHead().GetRepo().GetOwner().GetLogin(), Ref: pr.GetHead().GetSHA(), Base: pr.GetBase().GetRef(), URL: pr.GetHTMLURL(), Draft: pr.GetDraft()}
}

type PRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Patch     string `json:"patch,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type Comment struct {
	ID      int64  `json:"id"`
	ReplyTo int64  `json:"replyTo,omitempty"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Body    string `json:"body"`
	Author  string `json:"author"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func pageOf[A, B any](rows []A, resp *github.Response, convert func(A) B) Page[B] {
	page := Page[B]{Items: make([]B, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, convert(row))
	}
	if resp.NextPage > 0 {
		page.NextCursor = strconv.Itoa(resp.NextPage)
	}
	return page
}

func newClient(ctx context.Context, mode string) (*github.Client, error) {
	client := github.NewClient(&http.Client{Timeout: 25 * time.Second})
	if mode == "public" {
		return client, nil
	}
	var token string
	switch mode {
	case "env":
		token = os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	case "cli":
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
		out, err := cmd.Output()
		if err != nil {
			return nil, errors.New("sign in with gh auth login on the runtime host first")
		}
		token = strings.TrimSpace(string(out))
	default:
		return nil, errors.New("choose a supported GitHub credential")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("set GH_TOKEN or GITHUB_TOKEN on the runtime host first")
	}
	return client.WithAuthToken(token), nil
}

type repository struct {
	client      *github.Client
	owner, name string
}

func (repo repository) read(ctx context.Context, kind string, number int, cursor string) (any, error) {
	page := 1
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 1 || n > 1000000 {
			return nil, errors.New("invalid page cursor")
		}
		page = n
	}
	opt := github.ListOptions{Page: page, PerPage: 20}
	pulls := repo.client.PullRequests
	if kind != "pulls" && number < 1 {
		return nil, errors.New("a positive pull request number is required")
	}
	switch kind {
	case "pulls":
		prs, resp, err := pulls.List(ctx, repo.owner, repo.name, &github.PullRequestListOptions{State: "open", ListOptions: opt})
		if err != nil {
			return nil, err
		}
		return pageOf(prs, resp, pullRequest), nil
	case "pull":
		pr, _, err := pulls.Get(ctx, repo.owner, repo.name, number)
		if err != nil {
			return nil, err
		}
		return pullRequest(pr), nil
	case "files":
		files, resp, err := pulls.ListFiles(ctx, repo.owner, repo.name, number, &opt)
		if err != nil {
			return nil, err
		}
		return pageOf(files, resp, func(f *github.CommitFile) PRFile {
			return PRFile{Filename: f.GetFilename(), Status: f.GetStatus(), Patch: f.GetPatch(), Additions: f.GetAdditions(), Deletions: f.GetDeletions()}
		}), nil
	case "comments":
		rows, resp, err := pulls.ListComments(ctx, repo.owner, repo.name, number, &github.PullRequestListCommentsOptions{ListOptions: opt})
		if err != nil {
			return nil, err
		}
		return pageOf(rows, resp, func(c *github.PullRequestComment) Comment {
			return Comment{ID: c.GetID(), ReplyTo: c.GetInReplyTo(), Path: c.GetPath(), Line: c.GetLine(), Body: c.GetBody(), Author: c.GetUser().GetLogin()}
		}), nil
	}
	return nil, fmt.Errorf("unknown GitHub operation %q", kind)
}

var ErrStale = errors.New("the repository or pull request has changed; refresh and review the current revision")

func (repo repository) postReview(ctx context.Context, number int, head, body string) (any, error) {
	if number < 1 || head == "" || strings.TrimSpace(body) == "" || len(body) > 65536 {
		return nil, errors.New("provide a pull request, reviewed commit and a review of up to 65536 bytes")
	}
	pr, _, err := repo.client.PullRequests.Get(ctx, repo.owner, repo.name, number)
	if err != nil {
		return nil, err
	}
	if pr.GetHead().GetSHA() != head || pr.GetState() != "open" {
		return nil, ErrStale
	}
	review := &github.PullRequestReviewRequest{CommitID: &head, Event: github.String("COMMENT"), Body: &body}
	result, _, err := repo.client.PullRequests.CreateReview(ctx, repo.owner, repo.name, number, review)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": result.GetID(), "url": result.GetHTMLURL()}, nil
}
