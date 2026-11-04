package externalagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	InstallIdle      = "idle"
	InstallRunning   = "installing"
	InstallSucceeded = "installed"
	InstallFailed    = "failed"
)

const (
	ProblemNodeMissing   = "node_missing"
	ProblemNodeTooOld    = "node_too_old"
	ProblemNpmMissing    = "npm_missing"
	ProblemInstallFailed = "install_failed"
)

const installTimeout = 10 * time.Minute

type Problem struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type installJob struct {
	state   string
	problem *Problem
	started time.Time
}

type PresetStatus struct {
	Preset
	Installed      bool     `json:"installed"`
	InstallState   string   `json:"installState"`
	InstallProblem *Problem `json:"installProblem,omitempty"`
	Agent          *Agent   `json:"agent,omitempty"`
	UpdateReady    bool     `json:"updateReady"`
	Node           NodeInfo `json:"node"`
}

type NodeInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Major   int    `json:"major,omitempty"`
}

func (s *Service) installDir(p Preset) string {
	return filepath.Join(s.deps.ContenoxDir, "external-agents", p.ID, p.Version)
}

func (s *Service) lookPath(file string) (string, error) {
	if s.deps.LookPath != nil {
		return s.deps.LookPath(file)
	}
	return exec.LookPath(file)
}

var nodeVersionPattern = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

func (s *Service) node(ctx context.Context) NodeInfo {
	path, err := s.lookPath("node")
	if err != nil {
		return NodeInfo{}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := hidden(exec.CommandContext(ctx, path, "--version")).Output()
	if err != nil {
		return NodeInfo{Found: true, Path: path}
	}
	m := nodeVersionPattern.FindStringSubmatch(string(out))
	if m == nil {
		return NodeInfo{Found: true, Path: path}
	}
	major, _ := strconv.Atoi(m[1])
	return NodeInfo{Found: true, Path: path, Version: m[0], Major: major}
}

func (s *Service) installed(p Preset) bool {
	dir := s.installDir(p)
	if _, err := os.Stat(filepath.Join(dir, p.entry)); err != nil {
		return false
	}
	return s.nativeInstalled(p, dir)
}

func (s *Service) nativeInstalled(p Preset, dir string) bool {
	if p.nativeEntry == "" {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, p.nativeEntry)); err == nil {
		return true
	}
	arch := nodeArch(goarch())
	musl := strings.Replace(p.nativeEntry, "-linux-"+arch, "-linux-"+arch+"-musl", 1)
	_, err := os.Stat(filepath.Join(dir, musl))
	return err == nil
}

func (s *Service) Presets(ctx context.Context) ([]PresetStatus, error) {
	node := s.node(ctx)
	out := make([]PresetStatus, 0, len(presets))
	for _, p := range presets {
		if err := s.syncPreset(ctx, p); err != nil {
			return nil, err
		}
		st := PresetStatus{Preset: p, Installed: s.installed(p), InstallState: InstallIdle, Node: node}
		s.mu.Lock()
		if job := s.installs[p.ID]; job != nil {
			st.InstallState = job.state
			st.InstallProblem = job.problem
		}
		s.mu.Unlock()
		if st.Installed && st.InstallState == InstallIdle {
			st.InstallState = InstallSucceeded
		}
		if a, err := s.Get(ctx, p.AgentName); err == nil {
			st.Agent = &a
			st.UpdateReady = a.Version != "" && a.Version != p.Version
		} else if !errors.Is(err, apiframework.ErrNotFound) {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) InstallPreset(ctx context.Context, id string) (PresetStatus, error) {
	p, ok := presetByID(id)
	if !ok {
		return PresetStatus{}, fmt.Errorf("preset %q: %w", id, apiframework.ErrNotFound)
	}
	s.mu.Lock()
	if job := s.installs[id]; job != nil && job.state == InstallRunning {
		s.mu.Unlock()
		return s.presetStatus(ctx, id)
	}
	s.installs[id] = &installJob{state: InstallRunning, started: time.Now()}
	s.mu.Unlock()

	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), installTimeout)
		defer cancel()
		problem := s.install(runCtx, p)
		s.mu.Lock()
		defer s.mu.Unlock()
		if problem != nil {
			s.installs[id] = &installJob{state: InstallFailed, problem: problem}
			return
		}
		s.installs[id] = &installJob{state: InstallSucceeded}
	}()
	return s.presetStatus(ctx, id)
}

func (s *Service) presetStatus(ctx context.Context, id string) (PresetStatus, error) {
	all, err := s.Presets(ctx)
	if err != nil {
		return PresetStatus{}, err
	}
	for _, st := range all {
		if st.ID == id {
			return st, nil
		}
	}
	return PresetStatus{}, fmt.Errorf("preset %q: %w", id, apiframework.ErrNotFound)
}

func (s *Service) install(ctx context.Context, p Preset) *Problem {
	reportErr, reportChange, end := s.deps.Tracker.Start(ctx, "install", "external_agent_preset", "preset", p.ID, "version", p.Version)
	defer end()
	fail := func(pr *Problem) *Problem {
		reportErr(errors.New(pr.Kind + ": " + pr.Message))
		return pr
	}

	node := s.node(ctx)
	if !node.Found {
		return fail(&Problem{Kind: ProblemNodeMissing, Message: fmt.Sprintf("Node.js %d or later is needed and was not found on PATH", p.MinNode)})
	}
	if node.Major < p.MinNode {
		return fail(&Problem{Kind: ProblemNodeTooOld, Message: fmt.Sprintf("Node.js %d or later is needed; found %s at %s", p.MinNode, node.Version, node.Path)})
	}
	npm, err := s.lookPath("npm")
	if err != nil {
		return fail(&Problem{Kind: ProblemNpmMissing, Message: "npm was not found on PATH next to Node.js"})
	}

	dir := s.installDir(p)
	if !s.installed(p) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail(&Problem{Kind: ProblemInstallFailed, Message: "could not create the install folder", Detail: err.Error()})
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"private\":true}\n"), 0o644); err != nil {
			return fail(&Problem{Kind: ProblemInstallFailed, Message: "could not prepare the install folder", Detail: err.Error()})
		}
		cmd := hidden(exec.CommandContext(ctx, npm, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--omit=dev", p.Package+"@"+p.Version))
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return fail(&Problem{Kind: ProblemInstallFailed, Message: "npm could not install " + p.Package + "@" + p.Version, Detail: tail(out.String(), 4000)})
		}
		if !s.installed(p) {
			return fail(&Problem{Kind: ProblemInstallFailed, Message: "the package installed but its agent files are missing", Detail: tail(out.String(), 4000)})
		}
	}

	if err := s.registerPreset(ctx, p, node.Path); err != nil {
		return fail(&Problem{Kind: ProblemInstallFailed, Message: "the agent could not be registered", Detail: err.Error()})
	}
	reportChange(p.AgentName, map[string]any{"dir": dir})
	return nil
}

func (s *Service) presetConfigDir(ctx context.Context, p Preset) string {
	if s.unofficialAllowed(ctx) {
		return filepath.Join(s.deps.Home, p.configDir)
	}
	return filepath.Join(s.deps.ContenoxDir, "external-agents", p.ID, "home")
}

func (s *Service) presetConfig(ctx context.Context, p Preset, nodePath string) runtimetypes.ExternalACPConfig {
	dir := s.installDir(p)
	configDir := s.presetConfigDir(ctx, p)
	env := map[string]string{p.configEnv: configDir}
	for k, v := range p.env {
		env[k] = v
	}
	var meta json.RawMessage
	if p.sessionMeta != "" {
		meta = json.RawMessage(p.sessionMeta)
	}
	return runtimetypes.ExternalACPConfig{
		Transport:   runtimetypes.ExternalACPTransportStdio,
		Command:     nodePath,
		Args:        []string{filepath.Join(dir, p.entry)},
		Env:         env,
		SessionMeta: meta,
		Paths: []runtimetypes.ExternalACPPath{
			{Path: configDir, Mode: runtimetypes.ExternalACPPathWrite, Needs: p.configNeeds},
			{Path: dir, Mode: runtimetypes.ExternalACPPathRead, Needs: "the installed " + p.Title + " agent"},
			{Path: filepath.Dir(nodePath), Mode: runtimetypes.ExternalACPPathRead, Needs: "the Node.js runtime the agent runs on"},
		},
		Modes: append([]string(nil), p.Modes...),
	}
}

func (s *Service) registerPreset(ctx context.Context, p Preset, nodePath string) error {
	cfg := s.presetConfig(ctx, p, nodePath)
	source := runtimetypes.AgentSourceRegistry
	id, version := p.ID, p.Version
	existing, err := s.deps.Agents.GetByName(ctx, p.AgentName)
	switch {
	case err == nil:
		if existing.Kind != runtimetypes.AgentKindExternalACP {
			return fmt.Errorf("an agent named %q already exists and is not an external agent", p.AgentName)
		}
		if prev, perr := existing.ExternalACPConfig(); perr == nil {
			cfg.McpServers = prev.McpServers
		}
		if err := existing.SetExternalACPConfig(cfg); err != nil {
			return err
		}
		existing.Source, existing.RegistryID, existing.RegistryVersion = &source, &id, &version
		return s.deps.Agents.Update(ctx, existing)
	case errors.Is(err, libdb.ErrNotFound):
		a := &runtimetypes.Agent{Name: p.AgentName, Enabled: true, Source: &source, RegistryID: &id, RegistryVersion: &version}
		if err := a.SetExternalACPConfig(cfg); err != nil {
			return err
		}
		return s.deps.Agents.Create(ctx, a)
	default:
		return err
	}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
