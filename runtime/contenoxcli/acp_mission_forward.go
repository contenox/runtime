package contenoxcli

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	missionForwardProbeTTL      = 1 * time.Second
	missionForwardHealthTimeout = 1500 * time.Millisecond
)

type missionForwarder struct {
	getenv     func(string) string
	httpClient *http.Client
	now        func() time.Time
	probeTTL   time.Duration

	mu          sync.Mutex
	cachedReach bool
	cachedAt    time.Time
	haveCache   bool
	lastTarget  string
}

var (
	_ acpsvc.MissionDispatcher    = (*missionForwarder)(nil)
	_ acpsvc.MissionAgentResolver = (*missionForwarder)(nil)
)

func newMissionForwarder() *missionForwarder {
	return &missionForwarder{
		getenv:     os.Getenv,
		httpClient: &http.Client{Timeout: serveClientTimeout},
		now:        time.Now,
		probeTTL:   missionForwardProbeTTL,
	}
}

func (f *missionForwarder) forwardConfig() *acpsvc.MissionForwardConfig {
	return &acpsvc.MissionForwardConfig{
		Reachable: f.Reachable,
		TargetURL: f.TargetURL,
	}
}

func (f *missionForwarder) discover(_ context.Context) (baseURL, token string) {
	token = strings.TrimSpace(f.getenv(envServeToken))
	return normalizeBaseURL(strings.TrimSpace(f.getenv(envServeURL))), token
}

func (f *missionForwarder) Reachable() bool {
	f.mu.Lock()
	if f.haveCache && f.now().Sub(f.cachedAt) < f.probeTTL {
		reach := f.cachedReach
		f.mu.Unlock()
		return reach
	}
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), missionForwardHealthTimeout)
	defer cancel()
	baseURL, _ := f.discover(ctx)
	reach := baseURL != "" && f.probeHealth(ctx, baseURL)

	f.mu.Lock()
	f.cachedReach, f.cachedAt, f.haveCache, f.lastTarget = reach, f.now(), true, baseURL
	f.mu.Unlock()
	return reach
}

func (f *missionForwarder) probeHealth(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (f *missionForwarder) TargetURL() string {
	f.mu.Lock()
	if f.lastTarget != "" {
		t := f.lastTarget
		f.mu.Unlock()
		return t
	}
	f.mu.Unlock()

	baseURL, _ := f.discover(context.Background())
	f.mu.Lock()
	f.lastTarget = baseURL
	f.mu.Unlock()
	return baseURL
}

func (f *missionForwarder) Dispatch(ctx context.Context, req fleetservice.DispatchRequest) (fleetservice.DispatchResult, error) {
	baseURL, token := f.discover(ctx)
	return f.clientAt(baseURL, token).dispatchMission(ctx, req)
}

func (f *missionForwarder) GetByName(ctx context.Context, name string) (*runtimetypes.Agent, error) {
	baseURL, token := f.discover(ctx)
	return f.clientAt(baseURL, token).getAgentByName(ctx, name)
}

func (f *missionForwarder) clientAt(baseURL, token string) *serveClient {
	return &serveClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		http:    f.httpClient,
	}
}

func (c *serveClient) getAgentByName(ctx context.Context, name string) (*runtimetypes.Agent, error) {
	var out runtimetypes.Agent
	if err := c.get(ctx, "/agents/by-name/"+url.PathEscape(name), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func normalizeBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return u
	}
	if !strings.Contains(u, "://") {
		return "http://" + u
	}
	return u
}
