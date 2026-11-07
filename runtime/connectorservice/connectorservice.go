package connectorservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/errdefs"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

const (
	KindBuiltin = "builtin"
	KindMCP     = "mcp"
	KindOpenAPI = "openapi"

	ScopeEverywhere = "everywhere"
	ScopeWorkspace  = "workspace"
	ScopeSession    = "session"
	ScopeAgent      = "agent"

	StatusReady       = "ready"
	StatusNeedsSignIn = "needs_sign_in"
	StatusUnavailable = "unavailable"

	disabledKVPrefix = "session-connectors-off:"
	listConcurrency  = 8
	toolListTimeout  = 20 * time.Second
	pageSize         = 100
)

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Connector struct {
	Name             string              `json:"name"`
	Label            string              `json:"label"`
	Kind             string              `json:"kind"`
	ID               string              `json:"id,omitempty"`
	Transport        string              `json:"transport,omitempty"`
	Command          string              `json:"command,omitempty"`
	Args             []string            `json:"args,omitempty"`
	URL              string              `json:"url,omitempty"`
	AuthType         string              `json:"authType,omitempty"`
	Scope            string              `json:"scope"`
	Workspace        string              `json:"workspace,omitempty"`
	Status           string              `json:"status"`
	Problem          string              `json:"problem,omitempty"`
	Tools            []Tool              `json:"tools"`
	Enabled          *bool               `json:"enabled,omitempty"`
	Hidden           map[string][]string `json:"hidden,omitempty"`
	HiddenBySettings []string            `json:"hiddenBySettings,omitempty"`
	HiddenInChat     []string            `json:"hiddenInChat,omitempty"`
}

type Filter struct {
	Cwd          string
	SessionNames []string
	SessionID    string
	Session      bool
}

type Service interface {
	List(ctx context.Context, f Filter) ([]Connector, error)
	Check(ctx context.Context, name string) (Connector, error)
}

type service struct {
	db    libdb.DBManager
	tools taskengine.ToolsProvider
}

func New(db libdb.DBManager, tools taskengine.ToolsProvider) Service {
	return &service{db: db, tools: tools}
}

type source struct {
	mcp    map[string]*runtimetypes.MCPServer
	remote map[string]*runtimetypes.RemoteTools
}

func (s *service) store() runtimetypes.Store {
	return runtimetypes.New(s.db.WithoutTransaction())
}

func (s *service) sources(ctx context.Context) (source, error) {
	src := source{mcp: map[string]*runtimetypes.MCPServer{}, remote: map[string]*runtimetypes.RemoteTools{}}
	servers, err := AllMCPServers(ctx, s.store())
	if err != nil {
		return src, err
	}
	for _, m := range servers {
		src.mcp[m.Name] = m
	}
	remotes, err := AllRemoteTools(ctx, s.store())
	if err != nil {
		return src, err
	}
	for _, r := range remotes {
		src.remote[r.Name] = r
	}
	return src, nil
}

func AllMCPServers(ctx context.Context, store runtimetypes.Store) ([]*runtimetypes.MCPServer, error) {
	var out []*runtimetypes.MCPServer
	var cursor *time.Time
	var cursorID string
	for {
		page, err := store.ListMCPServers(ctx, cursor, pageSize, cursorID)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
		cursor = &page[len(page)-1].CreatedAt
		cursorID = page[len(page)-1].ID
	}
}

func AllRemoteTools(ctx context.Context, store runtimetypes.Store) ([]*runtimetypes.RemoteTools, error) {
	var out []*runtimetypes.RemoteTools
	var cursor *time.Time
	var cursorID string
	for {
		page, err := store.ListRemoteTools(ctx, cursor, pageSize, cursorID)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
		cursor = &page[len(page)-1].CreatedAt
		cursorID = page[len(page)-1].ID
	}
}

func describe(name string, src source) Connector {
	if m, ok := src.mcp[name]; ok {
		c := Connector{
			Name:      m.Name,
			Kind:      KindMCP,
			ID:        m.ID,
			Transport: m.Transport,
			Command:   m.Command,
			Args:      m.Args,
			URL:       m.URL,
			AuthType:  m.AuthType,
			Workspace: m.Workspace,
		}
		switch {
		case runtimetypes.IsACPManagedMCPServerName(m.Name):
			c.Scope = ScopeSession
			c.Label = sessionLabel(m.Name)
		case runtimetypes.IsDeclaredToolName(m.Name):
			c.Scope = ScopeAgent
		case m.Workspace != "":
			c.Scope = ScopeWorkspace
		default:
			c.Scope = ScopeEverywhere
		}
		return c
	}
	if r, ok := src.remote[name]; ok {
		c := Connector{Name: r.Name, Kind: KindOpenAPI, ID: r.ID, URL: r.EndpointURL, Workspace: r.Workspace}
		switch {
		case runtimetypes.IsDeclaredToolName(r.Name):
			c.Scope = ScopeAgent
		case r.Workspace != "":
			c.Scope = ScopeWorkspace
		default:
			c.Scope = ScopeEverywhere
		}
		return c
	}
	return Connector{Name: name, Kind: KindBuiltin, Scope: ScopeEverywhere}
}

func sessionLabel(name string) string {
	rest := strings.TrimPrefix(name, runtimetypes.ACPMCPServerNamePrefix)
	if i := strings.Index(rest, "-"); i >= 0 && i+1 < len(rest) {
		return rest[i+1:]
	}
	return name
}

func include(c Connector, f Filter) bool {
	if !f.Session {
		return c.Scope != ScopeSession
	}
	switch c.Scope {
	case ScopeSession:
		for _, n := range f.SessionNames {
			if n == c.Name {
				return true
			}
		}
		return false
	case ScopeAgent:
		return false
	case ScopeWorkspace:
		return runtimetypes.WorkspaceCovers(c.Workspace, f.Cwd)
	}
	return true
}

func (s *service) List(ctx context.Context, f Filter) ([]Connector, error) {
	if s.tools == nil {
		return nil, errors.New("connectors: tools registry is not configured")
	}
	if f.Session {
		ctx = taskengine.WithRuntimeToolsAllowlist(ctx, append([]string{"*"}, f.SessionNames...))
	}
	src, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	names, err := s.tools.Supports(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectors: list providers: %w", err)
	}
	var picked []Connector
	for _, name := range names {
		c := describe(name, src)
		if c.Label == "" {
			c.Label = c.Name
		}
		if include(c, f) {
			picked = append(picked, c)
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, listConcurrency)
	for i := range picked {
		wg.Add(1)
		sem <- struct{}{}
		go func(c *Connector) {
			defer wg.Done()
			defer func() { <-sem }()
			s.probe(ctx, c)
		}(&picked[i])
	}
	wg.Wait()
	s.markHidden(ctx, picked, f)
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].Kind == KindBuiltin && picked[j].Kind != KindBuiltin {
			return false
		}
		if picked[i].Kind != KindBuiltin && picked[j].Kind == KindBuiltin {
			return true
		}
		return strings.ToLower(picked[i].Name) < strings.ToLower(picked[j].Name)
	})
	return picked, nil
}

func (s *service) Check(ctx context.Context, name string) (Connector, error) {
	if s.tools == nil {
		return Connector{}, errors.New("connectors: tools registry is not configured")
	}
	name = strings.TrimSpace(name)
	src, err := s.sources(ctx)
	if err != nil {
		return Connector{}, err
	}
	names, err := s.tools.Supports(ctx)
	if err != nil {
		return Connector{}, err
	}
	for _, n := range names {
		if n == name {
			c := describe(n, src)
			if c.Label == "" {
				c.Label = c.Name
			}
			s.probe(ctx, &c)
			one := []Connector{c}
			s.markHidden(ctx, one, Filter{})
			return one[0], nil
		}
	}
	return Connector{}, fmt.Errorf("%w: connector %q", errdefs.ErrNotFound, name)
}

func (s *service) probe(ctx context.Context, c *Connector) {
	tctx, cancel := context.WithTimeout(ctx, toolListTimeout)
	defer cancel()
	tools, err := s.tools.GetToolsForToolsByName(tctx, c.Name)
	c.Tools = []Tool{}
	if err != nil {
		c.Status, c.Problem = classify(err)
		return
	}
	c.Status = StatusReady
	for _, t := range tools {
		c.Tools = append(c.Tools, Tool{Name: t.Function.Name, Description: t.Function.Description})
	}
}

func classify(err error) (string, string) {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 400 {
		msg = msg[:397] + "..."
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "not authenticated") || (strings.Contains(lower, "oauth") && strings.Contains(lower, "token")) {
		return StatusNeedsSignIn, msg
	}
	return StatusUnavailable, msg
}

func disabledKey(sessionID string) string {
	return disabledKVPrefix + sessionID
}

func Disabled(ctx context.Context, store runtimetypes.Store, sessionID string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	var names []string
	if err := store.GetKV(ctx, disabledKey(sessionID), &names); err != nil {
		return nil
	}
	return names
}

func SetEnabled(ctx context.Context, store runtimetypes.Store, sessionID, name string, enabled bool) error {
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" || name == "" {
		return fmt.Errorf("%w: session and connector name are required", errdefs.ErrUnprocessableEntity)
	}
	current := Disabled(ctx, store, sessionID)
	next := make([]string, 0, len(current)+1)
	for _, n := range current {
		if n != name {
			next = append(next, n)
		}
	}
	if !enabled {
		next = append(next, name)
	}
	if len(next) == 0 {
		err := store.DeleteKV(ctx, disabledKey(sessionID))
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return store.SetKV(ctx, disabledKey(sessionID), raw)
}

func ForgetSession(ctx context.Context, store runtimetypes.Store, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = store.DeleteKV(ctx, disabledKey(sessionID))
}

func Allowlist(ctx context.Context, store runtimetypes.Store, cwd, sessionID string, sessionNames []string) ([]string, error) {
	allow := []string{"*"}
	own := make(map[string]struct{}, len(sessionNames))
	for _, n := range sessionNames {
		own[n] = struct{}{}
	}
	servers, err := AllMCPServers(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("connectors: list mcp servers for allowlist: %w", err)
	}
	for _, srv := range servers {
		if runtimetypes.IsACPManagedMCPServerName(srv.Name) {
			if _, ok := own[srv.Name]; !ok {
				allow = append(allow, "!"+srv.Name)
			}
			continue
		}
		if !runtimetypes.WorkspaceCovers(srv.Workspace, cwd) {
			allow = append(allow, "!"+srv.Name)
		}
	}
	remotes, err := AllRemoteTools(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("connectors: list remote tools for allowlist: %w", err)
	}
	for _, r := range remotes {
		if !runtimetypes.WorkspaceCovers(r.Workspace, cwd) {
			allow = append(allow, "!"+r.Name)
		}
	}
	for _, n := range Disabled(ctx, store, sessionID) {
		allow = append(allow, "!"+n)
	}
	for _, n := range hiddenFor(ctx, store, cwd) {
		allow = append(allow, "!"+n)
	}
	for _, n := range SessionHidden(ctx, store, sessionID) {
		allow = append(allow, "!"+n)
	}
	return allow, nil
}
