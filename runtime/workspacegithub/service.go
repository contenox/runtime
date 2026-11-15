package workspacegithub

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	git "github.com/go-git/go-git/v5"
	"github.com/google/go-github/v58/github"
)

type Remote struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
}
type Binding struct {
	Remote
	ID         int64  `json:"id"`
	Credential string `json:"credential"`
}
type State struct {
	Remotes []Remote `json:"remotes"`
	Branch  string   `json:"branch,omitempty"`
	Binding *Binding `json:"binding"`
}
type Service struct {
	Store       runtimetypes.Store
	Scope       func(string) string
	GrantedRoot func(string) (string, error)
	client      func(context.Context, string) (*github.Client, error)
}

var repoName = regexp.MustCompile(`^[a-zA-Z0-9-]+/[a-zA-Z0-9._-]+$`)

func githubRemote(raw string) string {
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = "ssh://git@github.com/" + strings.TrimPrefix(raw, "git@github.com:")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || (u.Scheme != "https" && u.Scheme != "ssh") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	name := strings.TrimSuffix(strings.Trim(strings.TrimSpace(u.Path), "/"), ".git")
	if !repoName.MatchString(name) || strings.HasSuffix(name, "/.") || strings.HasSuffix(name, "/..") {
		return ""
	}
	return name
}
func (s Service) Binding(ctx context.Context, cwd string) (*Binding, error) {
	if cwd == "" {
		return nil, errors.New("select a workspace")
	}
	root, err := s.GrantedRoot(cwd)
	if err != nil {
		return nil, err
	}
	var b Binding
	err = s.Store.GetWorkspaceKV(ctx, s.Scope(root), "github.repository", &b)
	if errors.Is(err, libdbexec.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}
func (s Service) State(ctx context.Context, root string) (State, error) {
	state := State{Remotes: []Remote{}}
	binding, err := s.Binding(ctx, root)
	if err != nil {
		return state, err
	}
	state.Binding = binding
	repo, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{EnableDotGitCommonDir: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if ref, err := repo.Head(); err == nil && ref.Name().IsBranch() {
		state.Branch = ref.Name().Short()
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return state, err
	}
	for _, remote := range remotes {
		cfg := remote.Config()
		if len(cfg.URLs) > 0 {
			if name := githubRemote(cfg.URLs[0]); name != "" {
				state.Remotes = append(state.Remotes, Remote{Name: cfg.Name, Repo: name})
			}
		}
	}
	sort.Slice(state.Remotes, func(i, j int) bool { return state.Remotes[i].Name < state.Remotes[j].Name })
	return state, nil
}
func (s Service) api(ctx context.Context, mode string) (*github.Client, error) {
	if s.client != nil {
		return s.client(ctx, mode)
	}
	return newClient(ctx, mode)
}
func (s Service) Connect(ctx context.Context, root string, selected Remote, mode string) (*Binding, error) {
	state, err := s.State(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, remote := range state.Remotes {
		if remote != selected {
			continue
		}
		client, err := s.api(ctx, mode)
		if err != nil {
			return nil, err
		}
		owner, name, _ := strings.Cut(remote.Repo, "/")
		repo, _, err := client.Repositories.Get(ctx, owner, name)
		if err != nil {
			return nil, err
		}
		if repo.GetID() == 0 {
			return nil, errors.New("missing GitHub repository ID")
		}
		b := Binding{Remote: remote, ID: repo.GetID(), Credential: mode}
		data, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		return &b, s.Store.SetWorkspaceKV(ctx, s.Scope(root), "github.repository", data)
	}
	return nil, errors.New("select a current GitHub remote from this workspace")
}
func (s Service) resolve(ctx context.Context, cwd string, expected int64) (repository, error) {
	b, err := s.Binding(ctx, cwd)
	if err != nil {
		return repository{}, err
	}
	if b == nil {
		return repository{}, errors.New("connect this workspace to GitHub first")
	}
	if expected != 0 && expected != b.ID {
		return repository{}, ErrStale
	}
	client, err := s.api(ctx, b.Credential)
	if err != nil {
		return repository{}, err
	}
	repo, _, err := client.Repositories.GetByID(ctx, b.ID)
	if err != nil {
		return repository{}, err
	}
	return repository{client: client, owner: repo.GetOwner().GetLogin(), name: repo.GetName()}, nil
}
func (s Service) Read(ctx context.Context, cwd string, id int64, kind string, number int, cursor string) (any, error) {
	r, err := s.resolve(ctx, cwd, id)
	if err != nil {
		return nil, err
	}
	return r.read(ctx, kind, number, cursor)
}
