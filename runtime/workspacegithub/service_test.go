package workspacegithub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/google/go-github/v58/github"
	"github.com/stretchr/testify/require"
)

func TestRemoteParsing(t *testing.T) {
	for _, v := range []string{"git@github.com:acme/project.git", "ssh://git@github.com/acme/project.git", "https://github.com/acme/project"} {
		require.Equal(t, "acme/project", githubRemote(v))
	}
	for _, v := range []string{"https://github.com.example/acme/repo", "https://github.com@evil.test/acme/repo", "file:///acme/repo", "https://github.com/../", "https://gitlab.com/acme/repo", "https://github.com/acme/..", "https://github.com/acme/a/b"} {
		require.Empty(t, githubRemote(v))
	}
}

func TestRepositoryReview(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	other := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "upstream", URLs: []string{"git@github.com:acme/project.git"}})
	require.NoError(t, err)
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "qa.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	posts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/project", "/repositories/37":
			fmt.Fprint(w, `{"id":37,"name":"project","owner":{"login":"acme"}}`)
		case "/repos/acme/project/pulls":
			require.Equal(t, "20", r.URL.Query().Get("per_page"))
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://api.github.com/repos/acme/project/pulls?page=2>; rel="next"`)
			}
			fmt.Fprint(w, `[{"number":9,"title":"fix"}]`)
		case "/repos/acme/project/pulls/9":
			fmt.Fprint(w, `{"number":9,"state":"open","head":{"sha":"abc"}}`)
		case "/repos/acme/project/pulls/9/reviews":
			var review github.PullRequestReviewRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&review))
			require.Equal(t, "abc", review.GetCommitID())
			require.Equal(t, "COMMENT", review.GetEvent())
			posts++
			fmt.Fprint(w, `{"id":3}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	api := github.NewClient(upstream.Client())
	api.BaseURL, err = url.Parse(upstream.URL + "/")
	require.NoError(t, err)
	s := Service{Store: runtimetypes.New(db.WithoutTransaction()), Scope: func(root string) string { return root }, GrantedRoot: func(cwd string) (string, error) {
		if cwd == root || cwd == other {
			return cwd, nil
		}
		return "", errors.New("ungranted")
	}, client: func(context.Context, string) (*github.Client, error) { return api, nil }}
	_, err = s.Connect(ctx, root, Remote{"upstream", "other/repo"}, "public")
	require.Error(t, err)
	b, err := s.Connect(ctx, root, Remote{"upstream", "acme/project"}, "public")
	require.NoError(t, err)
	require.EqualValues(t, 37, b.ID)
	_, err = s.Read(ctx, other, 0, "pulls", 0, "")
	require.Error(t, err)
	_, err = s.Read(ctx, root, 38, "pulls", 0, "")
	require.ErrorIs(t, err, ErrStale)
	first, err := s.Read(ctx, root, 37, "pulls", 0, "")
	require.NoError(t, err)
	require.Equal(t, "2", first.(Page[PullRequest]).NextCursor)
	next, err := s.Read(ctx, root, 37, "pulls", 0, "2")
	require.NoError(t, err)
	require.Empty(t, next.(Page[PullRequest]).NextCursor)
	r, err := s.resolve(ctx, root, 37)
	require.NoError(t, err)
	_, err = r.postReview(ctx, 9, "old", "Finding")
	require.ErrorIs(t, err, ErrStale)
	require.Zero(t, posts)
	_, err = r.postReview(ctx, 9, "abc", "Finding")
	require.NoError(t, err)
	require.Equal(t, 1, posts)
	require.NoError(t, s.Store.DeleteWorkspaceKV(ctx, root, "github.repository"))
	_, err = s.Read(ctx, root, 0, "pull", 9, "")
	require.Error(t, err)
}
