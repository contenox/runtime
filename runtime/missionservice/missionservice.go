package missionservice

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	apiframework "github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

type EventPublisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

const ReportAddedSubject = "missionservice.events.report_added"

type ReportAddedEvent struct {
	MissionID       string `json:"missionId"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	Intent          string `json:"intent,omitempty"`
	Report          Report `json:"report"`
}

const (
	PlanRevisedSubject   = "missionservice.events.plan_revised"
	StatusChangedSubject = "missionservice.events.status_changed"
)

type PlanRevisedEvent struct {
	MissionID       string `json:"missionId"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	Intent          string `json:"intent,omitempty"`
	Revision        int    `json:"revision"`
	Explanation     string `json:"explanation,omitempty"`
	EntryCount      int    `json:"entryCount"`
	Added           int    `json:"added"`
	Removed         int    `json:"removed"`
	Pending         int    `json:"pending"`
	InProgress      int    `json:"inProgress"`
	Completed       int    `json:"completed"`
}

type PlanRevisionSummary struct {
	Revision    int       `json:"revision"`
	Explanation string    `json:"explanation,omitempty"`
	Added       int       `json:"added"`
	Removed     int       `json:"removed"`
	Pending     int       `json:"pending"`
	InProgress  int       `json:"inProgress"`
	Completed   int       `json:"completed"`
	At          time.Time `json:"at"`
}

type StatusChangedEvent struct {
	MissionID       string `json:"missionId"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	Intent          string `json:"intent,omitempty"`
	OldStatus       Status `json:"oldStatus"`
	NewStatus       Status `json:"newStatus"`
	Reason          string `json:"reason,omitempty"`
}

type Status string

const (
	StatusOpen      Status = "open"
	StatusLanded    Status = "landed"
	StatusDerailed  Status = "derailed"
	StatusAbandoned Status = "abandoned"

	StatusStuck Status = "stuck"
)

func IsTerminalStatus(status Status) bool {
	return isTerminalStatus(status)
}

func isTerminalStatus(status Status) bool {
	switch status {
	case StatusLanded, StatusDerailed, StatusStuck, StatusAbandoned:
		return true
	default:
		return false
	}
}

const missionKVPrefix = "fleet:mission:"

const missionReportKVPrefix = "fleet:mission_report:"

type Mission struct {
	ID              string                `json:"id"`
	Intent          string                `json:"intent"`
	AgentName       string                `json:"agentName"`
	HITLPolicyName  string                `json:"hitlPolicyName"`
	SessionID       string                `json:"sessionId,omitempty"`
	InstanceID      string                `json:"instanceId,omitempty"`
	ParentSessionID string                `json:"parentSessionId,omitempty"`
	Status          Status                `json:"status"`
	StatusReason    string                `json:"statusReason,omitempty"`
	Plan            Plan                  `json:"plan"`
	PlanRevisions   []PlanRevisionSummary `json:"planRevisions,omitempty"`
	LastHeartbeat   *time.Time            `json:"lastHeartbeat,omitempty"`
	LastError       string                `json:"lastError,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
}

type ReportKind string

const (
	ReportKindProgress ReportKind = "progress"
	ReportKindFinding  ReportKind = "finding"
	ReportKindBlocker  ReportKind = "blocker"
	ReportKindResult   ReportKind = "result"
)

type Report struct {
	ID        string     `json:"id"`
	MissionID string     `json:"missionId"`
	Kind      ReportKind `json:"kind"`
	Summary   string     `json:"summary"`
	Detail    string     `json:"detail,omitempty"`
	Refs      []string   `json:"refs,omitempty"`
	Handover  *Handover  `json:"handover,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Handover struct {
	Outcome         string   `json:"outcome,omitempty"`
	Artifacts       []string `json:"artifacts,omitempty"`
	HandoverForNext string   `json:"handoverForNext,omitempty"`
	Caveats         string   `json:"caveats,omitempty"`
}

func (h *Handover) IsEmpty() bool {
	if h == nil {
		return true
	}
	if strings.TrimSpace(h.Outcome) != "" ||
		strings.TrimSpace(h.HandoverForNext) != "" ||
		strings.TrimSpace(h.Caveats) != "" {
		return false
	}
	for _, a := range h.Artifacts {
		if strings.TrimSpace(a) != "" {
			return false
		}
	}
	return true
}

type PlanEntryStatus string

const (
	PlanEntryPending    PlanEntryStatus = "pending"
	PlanEntryInProgress PlanEntryStatus = "in_progress"
	PlanEntryCompleted  PlanEntryStatus = "completed"
)

type PlanEntryPriority string

const (
	PlanEntryPriorityHigh   PlanEntryPriority = "high"
	PlanEntryPriorityMedium PlanEntryPriority = "medium"
	PlanEntryPriorityLow    PlanEntryPriority = "low"
)

type PlanEntry struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Status   PlanEntryStatus   `json:"status"`
	Priority PlanEntryPriority `json:"priority"`
}

type Plan struct {
	Entries     []PlanEntry `json:"entries"`
	Revision    int         `json:"revision"`
	Explanation string      `json:"explanation,omitempty"`
}

type Service interface {
	Create(ctx context.Context, m *Mission) error
	Get(ctx context.Context, id string) (*Mission, error)

	GetByInstance(ctx context.Context, instanceID string) (*Mission, error)

	List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*Mission, error)
	Update(ctx context.Context, m *Mission) error
	Delete(ctx context.Context, id string) error

	Bind(ctx context.Context, id string, sessionID, instanceID string) (*Mission, error)

	Heartbeat(ctx context.Context, id string, lastErr string) (*Mission, error)

	Finish(ctx context.Context, id string, status Status, reason string) (*Mission, error)

	SetPlan(ctx context.Context, id string, entries []PlanEntry, explanation string) (*Mission, error)

	AddReport(ctx context.Context, missionID string, report *Report) error

	ListReports(ctx context.Context, missionID string, limit int) ([]*Report, error)
}

type service struct {
	db  libdb.DBManager
	pub EventPublisher
}

type Option func(*service)

func WithEventPublisher(pub EventPublisher) Option {
	return func(s *service) { s.pub = pub }
}

func New(db libdb.DBManager, opts ...Option) Service {
	s := &service{db: db}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *service) store() runtimetypes.Store {
	return runtimetypes.New(s.db.WithoutTransaction())
}

func (s *service) Create(ctx context.Context, m *Mission) error {
	if m == nil {
		return fmt.Errorf("mission is required")
	}
	m.Status = StatusOpen
	if err := validate(m); err != nil {
		return err
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	return s.put(ctx, m, false)
}

func (s *service) Get(ctx context.Context, id string) (*Mission, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	var m Mission
	if err := s.store().GetKV(ctx, missionKVPrefix+id, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

const scanPageSize = 200

func (s *service) GetByInstance(ctx context.Context, instanceID string) (*Mission, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instanceId is required")
	}
	var cursor *time.Time
	for {
		batch, next, err := s.listPage(ctx, cursor, scanPageSize)
		if err != nil {
			return nil, err
		}
		for _, m := range batch {
			if m.InstanceID == instanceID {
				return m, nil
			}
		}
		if len(batch) < scanPageSize || next == nil {
			return nil, libdb.ErrNotFound
		}
		if cursor != nil && !next.Before(*cursor) {
			return nil, libdb.ErrNotFound
		}
		cursor = next
	}
}

func (s *service) List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*Mission, error) {
	if limit <= 0 {
		limit = 100
	}
	missions, _, err := s.listPage(ctx, createdAtCursor, limit)
	return missions, err
}

func (s *service) listPage(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*Mission, *time.Time, error) {
	kvs, err := s.store().ListKVPrefix(ctx, missionKVPrefix, createdAtCursor, limit)
	if err != nil {
		return nil, nil, err
	}
	missions := make([]*Mission, 0, len(kvs))
	for _, kv := range kvs {
		var m Mission
		if err := json.Unmarshal(kv.Value, &m); err != nil {
			return nil, nil, fmt.Errorf("mission %q: %w", kv.Key, err)
		}
		missions = append(missions, &m)
	}
	var next *time.Time
	if len(kvs) > 0 {
		last := kvs[len(kvs)-1].CreatedAt
		next = &last
	}
	return missions, next, nil
}

func (s *service) Update(ctx context.Context, m *Mission) error {
	if m == nil {
		return fmt.Errorf("mission is required")
	}
	if m.ID == "" {
		return fmt.Errorf("id is required for update")
	}
	if err := validate(m); err != nil {
		return err
	}
	m.UpdatedAt = time.Now().UTC()
	return s.put(ctx, m, true)
}

func (s *service) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return s.store().DeleteKV(ctx, missionKVPrefix+id)
}

func (s *service) Bind(ctx context.Context, id string, sessionID, instanceID string) (*Mission, error) {
	if sessionID == "" && instanceID == "" {
		return nil, fmt.Errorf("bind requires a sessionId or instanceId")
	}
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	changed := false
	if sessionID != "" {
		switch m.SessionID {
		case "":
			m.SessionID = sessionID
			changed = true
		case sessionID:
		default:
			return nil, apiframework.Conflict(fmt.Sprintf("mission %q is already bound to session %q", id, m.SessionID))
		}
	}
	if instanceID != "" {
		switch m.InstanceID {
		case "":
			m.InstanceID = instanceID
			changed = true
		case instanceID:
		default:
			return nil, apiframework.Conflict(fmt.Sprintf("mission %q is already bound to instance %q", id, m.InstanceID))
		}
	}
	if !changed {
		return m, nil
	}

	m.UpdatedAt = time.Now().UTC()
	if err := s.put(ctx, m, true); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *service) Heartbeat(ctx context.Context, id string, lastErr string) (*Mission, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m.LastHeartbeat = &now
	m.LastError = lastErr
	m.UpdatedAt = now
	if err := s.put(ctx, m, true); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *service) put(ctx context.Context, m *Mission, mustExist bool) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal mission: %w", err)
	}
	if mustExist {
		return s.store().UpdateKV(ctx, missionKVPrefix+m.ID, raw)
	}
	return s.store().SetKV(ctx, missionKVPrefix+m.ID, raw)
}

func (s *service) AddReport(ctx context.Context, missionID string, report *Report) error {
	if missionID == "" {
		return fmt.Errorf("missionId is required")
	}
	if report == nil {
		return fmt.Errorf("report is required")
	}
	m, err := s.Get(ctx, missionID)
	if err != nil {
		return err
	}
	report.MissionID = missionID
	if report.Handover.IsEmpty() {
		report.Handover = nil
	}
	if err := validateReport(report); err != nil {
		return err
	}
	if report.ID == "" {
		report.ID = uuid.NewString()
	}
	report.CreatedAt = time.Now().UTC()

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := s.store().SetKV(ctx, missionReportKVPrefix+missionID+":"+report.ID, raw); err != nil {
		return err
	}
	s.publishReportAdded(ctx, m, report)
	return nil
}

func (s *service) publishReportAdded(ctx context.Context, m *Mission, report *Report) {
	if s.pub == nil {
		return
	}
	ev := ReportAddedEvent{
		MissionID:       m.ID,
		ParentSessionID: m.ParentSessionID,
		AgentName:       m.AgentName,
		Intent:          m.Intent,
		Report:          *report,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("missionservice: marshal report-added event failed; report stored, routing nudge skipped",
			"missionId", m.ID, "reportId", report.ID, "error", err)
		return
	}
	if err := s.pub.Publish(ctx, ReportAddedSubject, data); err != nil {
		slog.Warn("missionservice: publish report-added event failed; report stored, routing nudge skipped",
			"missionId", m.ID, "reportId", report.ID, "error", err)
	}
}

func (s *service) ListReports(ctx context.Context, missionID string, limit int) ([]*Report, error) {
	if missionID == "" {
		return nil, fmt.Errorf("missionId is required")
	}
	if limit <= 0 {
		limit = 100
	}
	kvs, err := s.store().ListKVPrefix(ctx, missionReportKVPrefix+missionID+":", nil, limit)
	if err != nil {
		return nil, err
	}
	reports := make([]*Report, 0, len(kvs))
	for _, kv := range kvs {
		var rep Report
		if err := json.Unmarshal(kv.Value, &rep); err != nil {
			return nil, fmt.Errorf("report %q: %w", kv.Key, err)
		}
		reports = append(reports, &rep)
	}
	return reports, nil
}

func (s *service) Finish(ctx context.Context, id string, status Status, reason string) (*Mission, error) {
	if !isTerminalStatus(status) {
		return nil, fmt.Errorf("cannot finish mission %q as %q: a terminal status is required (landed|derailed|stuck|abandoned)", id, status)
	}
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if isTerminalStatus(m.Status) {
		if m.Status == status {
			return m, nil
		}
		return nil, apiframework.Conflict(fmt.Sprintf("mission %q already finished as %q; cannot re-finish as %q", id, m.Status, status))
	}
	old := m.Status
	m.Status = status
	m.StatusReason = strings.TrimSpace(reason)
	m.UpdatedAt = time.Now().UTC()
	if err := s.put(ctx, m, true); err != nil {
		return nil, err
	}
	s.publishStatusChanged(ctx, m, old)
	return m, nil
}

func (s *service) SetPlan(ctx context.Context, id string, entries []PlanEntry, explanation string) (*Mission, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	normalized := make([]PlanEntry, len(entries))
	for i, e := range entries {
		e.Content = strings.TrimSpace(e.Content)
		if strings.TrimSpace(e.ID) == "" {
			e.ID = uuid.NewString()
		}
		normalized[i] = e
	}

	if err := validatePlan(normalized); err != nil {
		return nil, err
	}
	if err := validateCompletedImmutable(m.Plan.Entries, normalized); err != nil {
		return nil, err
	}

	prev := m.Plan.Entries
	m.Plan = Plan{
		Entries:     normalized,
		Revision:    m.Plan.Revision + 1,
		Explanation: strings.TrimSpace(explanation),
	}
	now := time.Now().UTC()
	m.UpdatedAt = now

	added, removed := planRevisionDelta(prev, m.Plan.Entries)
	pending, inProgress, completed := planStatusCounts(m.Plan.Entries)
	summary := PlanRevisionSummary{
		Revision:    m.Plan.Revision,
		Explanation: m.Plan.Explanation,
		Added:       added,
		Removed:     removed,
		Pending:     pending,
		InProgress:  inProgress,
		Completed:   completed,
		At:          now,
	}
	m.PlanRevisions = appendPlanRevision(m.PlanRevisions, summary)

	if err := s.put(ctx, m, true); err != nil {
		return nil, err
	}
	s.publishPlanRevised(ctx, m, summary)
	return m, nil
}

const maxPlanRevisions = 20

func appendPlanRevision(ring []PlanRevisionSummary, s PlanRevisionSummary) []PlanRevisionSummary {
	ring = append(ring, s)
	if len(ring) <= maxPlanRevisions {
		return ring
	}
	trimmed := make([]PlanRevisionSummary, maxPlanRevisions)
	copy(trimmed, ring[len(ring)-maxPlanRevisions:])
	return trimmed
}

func (s *service) publishStatusChanged(ctx context.Context, m *Mission, old Status) {
	if s.pub == nil {
		return
	}
	ev := StatusChangedEvent{
		MissionID:       m.ID,
		ParentSessionID: m.ParentSessionID,
		AgentName:       m.AgentName,
		Intent:          m.Intent,
		OldStatus:       old,
		NewStatus:       m.Status,
		Reason:          m.StatusReason,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("missionservice: marshal status-changed event failed; status stored, routing nudge skipped",
			"missionId", m.ID, "status", m.Status, "error", err)
		return
	}
	if err := s.pub.Publish(ctx, StatusChangedSubject, data); err != nil {
		slog.Warn("missionservice: publish status-changed event failed; status stored, routing nudge skipped",
			"missionId", m.ID, "status", m.Status, "error", err)
	}
}

func (s *service) publishPlanRevised(ctx context.Context, m *Mission, summary PlanRevisionSummary) {
	if s.pub == nil {
		return
	}
	ev := PlanRevisedEvent{
		MissionID:       m.ID,
		ParentSessionID: m.ParentSessionID,
		AgentName:       m.AgentName,
		Intent:          m.Intent,
		Revision:        summary.Revision,
		Explanation:     summary.Explanation,
		EntryCount:      len(m.Plan.Entries),
		Added:           summary.Added,
		Removed:         summary.Removed,
		Pending:         summary.Pending,
		InProgress:      summary.InProgress,
		Completed:       summary.Completed,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("missionservice: marshal plan-revised event failed; plan stored, routing nudge skipped",
			"missionId", m.ID, "revision", m.Plan.Revision, "error", err)
		return
	}
	if err := s.pub.Publish(ctx, PlanRevisedSubject, data); err != nil {
		slog.Warn("missionservice: publish plan-revised event failed; plan stored, routing nudge skipped",
			"missionId", m.ID, "revision", m.Plan.Revision, "error", err)
	}
}

func planRevisionDelta(prev, next []PlanEntry) (added, removed int) {
	prevIDs := make(map[string]bool, len(prev))
	for _, e := range prev {
		if e.ID != "" {
			prevIDs[e.ID] = true
		}
	}
	nextIDs := make(map[string]bool, len(next))
	for _, e := range next {
		if e.ID != "" {
			nextIDs[e.ID] = true
		}
	}
	for id := range nextIDs {
		if !prevIDs[id] {
			added++
		}
	}
	for id := range prevIDs {
		if !nextIDs[id] {
			removed++
		}
	}
	return added, removed
}

func planStatusCounts(entries []PlanEntry) (pending, inProgress, completed int) {
	for _, e := range entries {
		switch e.Status {
		case PlanEntryPending:
			pending++
		case PlanEntryInProgress:
			inProgress++
		case PlanEntryCompleted:
			completed++
		}
	}
	return pending, inProgress, completed
}

const (
	maxPlanEntries     = 100
	maxPlanEntryBytes  = 12000
	planEscapeRatioNum = 3
	planEscapeMinLen   = 400
)

func validatePlan(entries []PlanEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("a plan must have at least one entry")
	}
	if len(entries) > maxPlanEntries {
		return fmt.Errorf("plan has too many entries (%d, max %d)", len(entries), maxPlanEntries)
	}
	for i := range entries {
		e := &entries[i]
		if e.Content == "" {
			return fmt.Errorf("plan entry %d has empty content", i+1)
		}
		if len(e.Content) > maxPlanEntryBytes {
			return fmt.Errorf("plan entry %d exceeds max length (%d bytes, max %d)", i+1, len(e.Content), maxPlanEntryBytes)
		}
		if planContentLooksCorrupted(e.Content) {
			return fmt.Errorf("plan entry %d looks corrupted (stream or log paste); revise the plan or shorten the step", i+1)
		}
		if err := validatePlanEntryStatus(e.Status); err != nil {
			return fmt.Errorf("plan entry %d: %w", i+1, err)
		}
		if err := validatePlanEntryPriority(e.Priority); err != nil {
			return fmt.Errorf("plan entry %d: %w", i+1, err)
		}
	}
	return nil
}

func planContentLooksCorrupted(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "__next_f") || strings.Contains(lower, "self.__next_f") {
		return true
	}
	if len(s) >= planEscapeMinLen {
		if strings.Count(s, "\\")*planEscapeRatioNum > len(s) {
			return true
		}
	}
	return false
}

func validateCompletedImmutable(prev, next []PlanEntry) error {
	if len(prev) == 0 {
		return nil
	}
	completed := make(map[string]string, len(prev))
	for _, e := range prev {
		if e.Status == PlanEntryCompleted && e.ID != "" {
			completed[e.ID] = e.Content
		}
	}
	if len(completed) == 0 {
		return nil
	}
	for i := range next {
		e := &next[i]
		if e.ID == "" {
			continue
		}
		if prevContent, ok := completed[e.ID]; ok && e.Content != prevContent {
			return fmt.Errorf("plan entry %q rewrites the content of already-completed work; append a correction as a new entry instead", e.ID)
		}
	}
	return nil
}

func validatePlanEntryStatus(status PlanEntryStatus) error {
	switch status {
	case PlanEntryPending, PlanEntryInProgress, PlanEntryCompleted:
		return nil
	default:
		return fmt.Errorf("invalid plan entry status %q: must be one of pending|in_progress|completed", status)
	}
}

func validatePlanEntryPriority(priority PlanEntryPriority) error {
	switch priority {
	case PlanEntryPriorityHigh, PlanEntryPriorityMedium, PlanEntryPriorityLow:
		return nil
	default:
		return fmt.Errorf("invalid plan entry priority %q: must be one of high|medium|low", priority)
	}
}

func validate(m *Mission) error {
	if strings.TrimSpace(m.Intent) == "" {
		return fmt.Errorf("intent is required")
	}
	if strings.ContainsAny(m.Intent, "\r\n") {
		return fmt.Errorf("intent must be a single line")
	}
	if strings.TrimSpace(m.HITLPolicyName) == "" {
		return fmt.Errorf("hitlPolicyName is required: a mission must name its envelope")
	}
	return validateStatus(m.Status)
}

func validateStatus(status Status) error {
	switch status {
	case StatusOpen, StatusLanded, StatusDerailed, StatusStuck, StatusAbandoned:
		return nil
	default:
		return fmt.Errorf("invalid status %q: must be one of open|landed|derailed|stuck|abandoned", status)
	}
}

func validateReport(report *Report) error {
	if err := validateReportKind(report.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(report.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if strings.ContainsAny(report.Summary, "\r\n") {
		return fmt.Errorf("summary must be a single line")
	}
	return validateHandover(report.Handover)
}

const (
	maxHandoverTextBytes     = 8000
	maxHandoverArtifacts     = 50
	maxHandoverArtifactBytes = 2000
)

func validateHandover(h *Handover) error {
	if h == nil {
		return nil
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"outcome", h.Outcome},
		{"handoverForNext", h.HandoverForNext},
		{"caveats", h.Caveats},
	} {
		if len(f.value) > maxHandoverTextBytes {
			return fmt.Errorf("handover %s exceeds max length (%d bytes, max %d)", f.name, len(f.value), maxHandoverTextBytes)
		}
		if planContentLooksCorrupted(f.value) {
			return fmt.Errorf("handover %s looks corrupted (stream or log paste); shorten it or move it to an artifact reference", f.name)
		}
	}
	if len(h.Artifacts) > maxHandoverArtifacts {
		return fmt.Errorf("handover has too many artifacts (%d, max %d)", len(h.Artifacts), maxHandoverArtifacts)
	}
	for i, a := range h.Artifacts {
		if len(a) > maxHandoverArtifactBytes {
			return fmt.Errorf("handover artifact %d exceeds max length (%d bytes, max %d)", i+1, len(a), maxHandoverArtifactBytes)
		}
	}
	return nil
}

func validateReportKind(kind ReportKind) error {
	switch kind {
	case ReportKindProgress, ReportKindFinding, ReportKindBlocker, ReportKindResult:
		return nil
	default:
		return fmt.Errorf("invalid report kind %q: must be one of progress|finding|blocker|result", kind)
	}
}
