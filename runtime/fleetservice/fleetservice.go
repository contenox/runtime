package fleetservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/google/uuid"
)

type DispatchRequest struct {
	AgentName      string `json:"agentName"`
	Intent         string `json:"intent"`
	HITLPolicyName string `json:"hitlPolicyName"`
	Cwd            string `json:"cwd,omitempty"`

	ParentSessionID string `json:"parentSessionId,omitempty"`
}

type DispatchResult struct {
	InstanceID string `json:"instanceId"`
	SessionID  string `json:"sessionId"`
	MissionID  string `json:"missionId"`
}

type Service interface {
	List(ctx context.Context) ([]agentinstance.FleetEntry, error)

	Get(ctx context.Context, instanceID string) (agentinstance.InstanceStatus, error)

	Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error)

	Stop(ctx context.Context, instanceID string) error

	Cancel(ctx context.Context, instanceID, sessionID string) error
}

type service struct {
	instances       agentinstance.Manager
	agents          agentregistryservice.Service
	missions        missionservice.Service
	workspaceRoots  *vfs.Factory
	projectRoot     string
	tracker         libtracker.ActivityTracker
	computeBounds   hitlservice.ComputeBoundsReader
	policyValidator hitlservice.PolicyValidator
}

type Option func(*service)

func WithPolicyValidator(v hitlservice.PolicyValidator) Option {
	return func(s *service) { s.policyValidator = v }
}

func WithComputeBounds(r hitlservice.ComputeBoundsReader) Option {
	return func(s *service) { s.computeBounds = r }
}

func New(
	instances agentinstance.Manager,
	agents agentregistryservice.Service,
	missions missionservice.Service,
	workspaceRoots *vfs.Factory,
	projectRoot string,
	tracker libtracker.ActivityTracker,
	opts ...Option,
) Service {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	s := &service{
		instances:      instances,
		agents:         agents,
		missions:       missions,
		workspaceRoots: workspaceRoots,
		projectRoot:    projectRoot,
		tracker:        tracker,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *service) List(ctx context.Context) ([]agentinstance.FleetEntry, error) {
	return s.instances.List(ctx)
}

func (s *service) Get(ctx context.Context, instanceID string) (agentinstance.InstanceStatus, error) {
	_ = ctx
	return s.instances.Get(instanceID)
}

func (s *service) Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error) {
	if strings.TrimSpace(req.AgentName) == "" {
		return DispatchResult{}, apiframework.MissingParameter("agentName", "agentName is required")
	}
	if strings.TrimSpace(req.Intent) == "" {
		return DispatchResult{}, apiframework.MissingParameter("intent", "intent is required")
	}
	if strings.TrimSpace(req.HITLPolicyName) == "" {
		return DispatchResult{}, apiframework.MissingParameter("hitlPolicyName", "hitlPolicyName is required: a mission must name its envelope")
	}
	if s.policyValidator != nil {
		if err := s.policyValidator.ValidatePolicy(ctx, req.HITLPolicyName); err != nil {
			return DispatchResult{}, apiframework.InvalidParameterValue("hitlPolicyName",
				fmt.Sprintf("hitl policy %q could not be loaded: it must name an existing policy (see `contenox config` / the .contenox policy files)", strings.TrimSpace(req.HITLPolicyName)))
		}
	}
	cwd, err := s.resolveCwd(req.Cwd)
	if err != nil {
		return DispatchResult{}, err
	}

	agent, err := agentregistryservice.ResolveForSpawn(ctx, s.agents, req.AgentName)
	if err != nil {
		if errors.Is(err, agentregistryservice.ErrAgentDisabled) {
			return DispatchResult{}, apiframework.Conflict(err.Error())
		}
		return DispatchResult{}, err
	}

	missionID := uuid.NewString()

	instanceID, err := s.instances.StartResolved(ctx, agent, cwd)
	if err != nil {
		return DispatchResult{}, err
	}

	sessionID, err := s.instances.OpenSession(ctx, instanceID, agentinstance.SessionSpec{
		Cwd:  cwd,
		Meta: missionservice.MarshalMissionMeta(missionID),
	})
	if err != nil {
		_ = s.instances.Stop(instanceID)
		return DispatchResult{}, err
	}

	result := DispatchResult{InstanceID: instanceID, SessionID: string(sessionID)}

	m := &missionservice.Mission{
		ID:              missionID,
		Intent:          req.Intent,
		AgentName:       req.AgentName,
		HITLPolicyName:  req.HITLPolicyName,
		ParentSessionID: req.ParentSessionID,
	}
	if err := s.missions.Create(ctx, m); err != nil {
		_ = s.instances.Stop(instanceID)
		return DispatchResult{}, err
	}
	if _, err := s.missions.Bind(ctx, m.ID, string(sessionID), instanceID); err != nil {
		_ = s.instances.Stop(instanceID)
		return DispatchResult{}, err
	}
	result.MissionID = m.ID

	detached := context.WithoutCancel(ctx)
	go s.driveUnattendedMission(detached, missionRun{
		instanceID: instanceID,
		sessionID:  sessionID,
		missionID:  m.ID,
		agentName:  req.AgentName,
		intent:     req.Intent,
	})

	return result, nil
}

type missionRun struct {
	instanceID string
	sessionID  libacp.SessionID
	missionID  string
	agentName  string
	intent     string
}

func (s *service) driveUnattendedMission(ctx context.Context, run missionRun) {
	reportErr, reportChange, end := s.tracker.Start(ctx, "prompt", "fleet_dispatch",
		"instance_id", run.instanceID, "session_id", string(run.sessionID), "agent_name", run.agentName)
	defer end()

	bounds := s.computeBoundsFor(ctx, run.missionID)

	firstTurn := []libacp.ContentBlock{
		libacp.NewTextContent(missionPreamble),
		libacp.NewTextContent(run.intent),
	}
	stop, err := s.promptTurn(ctx, run, firstTurn)
	if err != nil {
		reportErr(err)
		return
	}
	reportChange(string(run.sessionID), string(stop))
	if s.missionReached(ctx, run.missionID) {
		return
	}
	if s.enforceTokenBudget(ctx, run, bounds, reportChange) {
		return
	}

	if turnBudgetExceeded(2, bounds) {
		s.finishComputeStuck(ctx, run.missionID, turnsExhaustedReason(bounds.MaxTurns), reportChange)
		return
	}
	stop, err = s.promptTurn(ctx, run, []libacp.ContentBlock{libacp.NewTextContent(missionNudge)})
	if err != nil {
		reportErr(err)
		return
	}
	reportChange(string(run.sessionID), string(stop))
	if s.missionReached(ctx, run.missionID) {
		return
	}
	if s.enforceTokenBudget(ctx, run, bounds, reportChange) {
		return
	}

	s.fileSilentTurnBlocker(ctx, run)
}

func (s *service) computeBoundsFor(ctx context.Context, missionID string) hitlservice.ComputeBounds {
	if s.computeBounds == nil {
		return hitlservice.ComputeBounds{}
	}
	m, err := s.missions.Get(ctx, missionID)
	if err != nil || m == nil {
		return hitlservice.ComputeBounds{}
	}
	bounds, err := s.computeBounds.ComputeBoundsFor(ctx, m.HITLPolicyName)
	if err != nil {
		return hitlservice.ComputeBounds{}
	}
	return bounds
}

func (s *service) enforceTokenBudget(ctx context.Context, run missionRun, b hitlservice.ComputeBounds, reportChange func(string, any)) bool {
	if b.MaxTokens <= 0 {
		return false
	}
	notes, ok := s.sessionJournal(run)
	if !ok {
		return false
	}
	used, present := journalTokenUsage(notes)
	if !present || !tokenBudgetExceeded(used, b) {
		return false
	}
	s.finishComputeStuck(ctx, run.missionID, tokensExhaustedReason(b.MaxTokens, used), reportChange)
	return true
}

func (s *service) finishComputeStuck(ctx context.Context, missionID, reason string, reportChange func(string, any)) {
	reportChange("compute_bound", reason)
	if _, err := s.missions.Finish(ctx, missionID, missionservice.StatusStuck, reason); err != nil {
		reportChange("compute_bound_finish_error", err.Error())
	}
}

type sessionJournalReader interface {
	SessionJournal(instanceID string, sessionID libacp.SessionID) ([]libacp.SessionNotification, string, bool)
}

func (s *service) sessionJournal(run missionRun) ([]libacp.SessionNotification, bool) {
	reader, ok := s.instances.(sessionJournalReader)
	if !ok {
		return nil, false
	}
	notes, _, owned := reader.SessionJournal(run.instanceID, run.sessionID)
	return notes, owned
}

func (s *service) promptTurn(ctx context.Context, run missionRun, blocks []libacp.ContentBlock) (libacp.StopReason, error) {
	stop, err := s.instances.Prompt(ctx, run.instanceID, run.sessionID, blocks)
	if err != nil {
		_, _ = s.missions.Heartbeat(ctx, run.missionID, err.Error())
		return "", err
	}
	_, _ = s.missions.Heartbeat(ctx, run.missionID, "")
	return stop, nil
}

func (s *service) missionReached(ctx context.Context, missionID string) bool {
	m, err := s.missions.Get(ctx, missionID)
	if err != nil {
		m = nil
	}
	reportCount := 0
	if reports, rerr := s.missions.ListReports(ctx, missionID, 1); rerr == nil {
		reportCount = len(reports)
	}
	return missionShowsUnitReached(m, reportCount)
}

func missionShowsUnitReached(m *missionservice.Mission, reportCount int) bool {
	if reportCount > 0 {
		return true
	}
	if m == nil {
		return false
	}
	if m.Status != missionservice.StatusOpen {
		return true
	}
	return m.Plan.Revision > 0
}

func (s *service) fileSilentTurnBlocker(ctx context.Context, run missionRun) {
	summary, detail := silentTurnBlocker(s.lastAgentText(run.instanceID, run.sessionID), string(run.sessionID))
	_ = s.missions.AddReport(ctx, run.missionID, &missionservice.Report{
		Kind:    missionservice.ReportKindBlocker,
		Summary: summary,
		Detail:  detail,
	})
}

type sessionTextReader interface {
	SessionAgentText(instanceID string, sessionID libacp.SessionID) (string, bool)
}

func (s *service) lastAgentText(instanceID string, sessionID libacp.SessionID) string {
	reader, ok := s.instances.(sessionTextReader)
	if !ok {
		return ""
	}
	text, _ := reader.SessionAgentText(instanceID, sessionID)
	return text
}

var (
	toolAskAttention = missiontools.ToolsProviderName + "." + missiontools.ToolNameAskAttention
	toolReport       = missiontools.ToolsProviderName + "." + missiontools.ToolNameReport
	toolFinish       = missiontools.ToolsProviderName + "." + missiontools.ToolNameFinish
)

var missionPreamble = fmt.Sprintf(`You are running as an UNATTENDED mission unit. No human is reading this conversation — replying in prose reaches no one. You reach your operator ONLY through your mission tools:
- %s: ask a question, or flag a blocker you must not decide alone.
- %s: record real progress, a finding, or a result.
- %s: end the mission with a verdict, once the work is truly done.
Do the work with your other tools. When you need the operator, or have something worth their attention, call a mission tool. Chat text alone will not be seen.`, toolAskAttention, toolReport, toolFinish)

var missionNudge = fmt.Sprintf(`Your last turn ended without reaching your operator, and no human is reading this chat. To reach your operator now, call %s (a question or a blocker) or %s (progress, a finding, or a result); to end the mission, call %s. If you are not done, keep working with your other tools and report when you have something. Do not answer in prose alone — it will not be seen.`, toolAskAttention, toolReport, toolFinish)

const silentTurnBlockerLead = "unit ended two turns without reporting"

func silentTurnBlocker(lastAgentText, sessionID string) (summary, detail string) {
	attach := fmt.Sprintf("The unit produced no mission report across two turns (its first turn and one runtime nudge). Attach to session %s to read its transcript and continue it.", sessionID)
	lastAgentText = strings.TrimSpace(lastAgentText)
	if lastAgentText == "" {
		return fmt.Sprintf("%s; attach to session %s", silentTurnBlockerLead, sessionID), attach
	}
	summary = singleLineExcerpt(fmt.Sprintf("%s — last said: %s", silentTurnBlockerLead, lastAgentText), 240)
	detail = fmt.Sprintf("The unit's last words:\n\n%s\n\n%s", lastAgentText, attach)
	return summary, detail
}

func singleLineExcerpt(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

func (s *service) Stop(ctx context.Context, instanceID string) error {
	_ = ctx
	return s.instances.Stop(instanceID)
}

func (s *service) Cancel(ctx context.Context, instanceID, sessionID string) error {
	_ = ctx
	if sessionID != "" {
		return s.instances.Cancel(instanceID, libacp.SessionID(sessionID))
	}
	status, err := s.instances.Get(instanceID)
	if err != nil {
		return err
	}
	var errs []error
	for _, sid := range status.SessionIDs {
		if cerr := s.instances.Cancel(instanceID, libacp.SessionID(sid)); cerr != nil {
			errs = append(errs, cerr)
		}
	}
	return errors.Join(errs...)
}

func (s *service) resolveCwd(cwd string) (string, error) {
	resolved, err := vfs.ResolveSessionCwd(s.workspaceRoots, cwd, s.projectRoot)
	if err != nil {
		return "", apiframework.InvalidParameterValue("cwd", err.Error())
	}
	return resolved, nil
}
