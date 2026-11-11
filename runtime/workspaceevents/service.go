package workspaceevents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
)

const (
	SourceEditor   = "editor"
	SourceWatcher  = "watcher"
	SourceTool     = "tool"
	SourceTerminal = "terminal"
	SourceApproval = "approval"
	SourceTrigger  = "trigger"
)

const (
	TypeFSCreated  = "fs.created"
	TypeFSModified = "fs.modified"
	TypeFSDeleted  = "fs.deleted"
	TypeFSMoved    = "fs.moved"
	TypeFSRescan   = "fs.rescan"

	TypeEditorOpened    = "editor.opened"
	TypeEditorClosed    = "editor.closed"
	TypeEditorSaved     = "editor.saved"
	TypeEditorDirty     = "editor.dirty"
	TypeEditorClean     = "editor.clean"
	TypeEditorSelection = "editor.selection"

	TypeTerminalStarted = "terminal.started"
	TypeTerminalExited  = "terminal.exited"

	TypeToolWrote = "tool.wrote"
	TypeToolRan   = "tool.ran"

	TypeApprovalDecided = "approval.decided"

	TypeTriggerStarted = "trigger.started"
	TypeTriggerSettled = "trigger.settled"
)

const (
	MaxDataBytes    = 4096
	MaxSubjectBytes = 1024

	coalesceWindow = time.Second
)

var (
	ErrUnknownType    = errors.New("workspaceevents: unknown event type")
	ErrSourceMismatch = errors.New("workspaceevents: event type does not belong to this source")
	ErrInvalidSubject = errors.New("workspaceevents: invalid subject")
	ErrInvalidData    = errors.New("workspaceevents: data must be a JSON object")
	ErrDataTooLarge   = errors.New("workspaceevents: data exceeds the size limit")
	ErrRootNotGranted = errors.New("workspaceevents: root is not a granted workspace root")
)

type typeSpec struct {
	source      string
	pathSubject bool
	coalesce    bool
}

var eventTypes = map[string]typeSpec{
	TypeFSCreated:  {source: SourceWatcher, pathSubject: true},
	TypeFSModified: {source: SourceWatcher, pathSubject: true},
	TypeFSDeleted:  {source: SourceWatcher, pathSubject: true},
	TypeFSMoved:    {source: SourceWatcher, pathSubject: true},
	TypeFSRescan:   {source: SourceWatcher},

	TypeEditorOpened:    {source: SourceEditor, pathSubject: true},
	TypeEditorClosed:    {source: SourceEditor, pathSubject: true},
	TypeEditorSaved:     {source: SourceEditor, pathSubject: true},
	TypeEditorDirty:     {source: SourceEditor, pathSubject: true},
	TypeEditorClean:     {source: SourceEditor, pathSubject: true},
	TypeEditorSelection: {source: SourceEditor, pathSubject: true, coalesce: true},

	TypeTerminalStarted: {source: SourceTerminal},
	TypeTerminalExited:  {source: SourceTerminal},

	TypeToolWrote: {source: SourceTool, pathSubject: true},
	TypeToolRan:   {source: SourceTool},

	TypeApprovalDecided: {source: SourceApproval},

	TypeTriggerStarted: {source: SourceTrigger},
	TypeTriggerSettled: {source: SourceTrigger},
}

type Event struct {
	NID     int64           `json:"nid"`
	Type    string          `json:"type"`
	Source  string          `json:"source"`
	Subject string          `json:"subject,omitempty"`
	Time    time.Time       `json:"time"`
	Data    json.RawMessage `json:"data"`
	Hop     int             `json:"hop,omitempty"`
}

type Input struct {
	Type    string
	Source  string
	Subject string
	Data    json.RawMessage
}

type Roots interface {
	Roots() []string
	Resolve(root string) (string, error)
}

type Service interface {
	GrantedRoot(candidate string) (string, error)
	Append(ctx context.Context, root string, in Input) (Event, error)
	AppendBatch(ctx context.Context, root string, ins []Input) ([]Event, error)
	Since(ctx context.Context, root string, afterNID int64, limit int) ([]Event, error)
	Latest(ctx context.Context, root string) (int64, error)
	Subscribe(ctx context.Context, root string, ch chan<- []byte) (libbus.Subscription, error)
}

type service struct {
	workspaceID string
	log         eventlog.Service
	bus         libbus.Messenger
	roots       Roots
	tracker     libtracker.ActivityTracker
	now         func() time.Time

	mu   sync.Mutex
	last map[string]coalesced
}

type coalesced struct {
	at    time.Time
	event Event
}

func New(workspaceID string, log eventlog.Service, bus libbus.Messenger, roots Roots, tracker libtracker.ActivityTracker) Service {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	return &service{
		workspaceID: strings.TrimSpace(workspaceID),
		log:         log,
		bus:         bus,
		roots:       roots,
		tracker:     tracker,
		now:         time.Now,
		last:        map[string]coalesced{},
	}
}

func rootKey(root string) string {
	key := filepath.ToSlash(filepath.Clean(root))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func (s *service) scope(root string) string {
	return s.workspaceID + ":" + rootKey(root)
}

func Scope(workspaceID, root string) string {
	return workspaceID + ":" + rootKey(root)
}

func Subject(workspaceID, root string) string {
	return "workspace.events." + workspaceID + "." + rootKey(root)
}

func (s *service) GrantedRoot(candidate string) (string, error) {
	if s.roots == nil {
		return "", ErrRootNotGranted
	}
	resolved, err := s.roots.Resolve(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRootNotGranted, err)
	}
	best := ""
	for _, root := range s.roots.Roots() {
		if vfs.Within(root, resolved) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w: %s", ErrRootNotGranted, candidate)
	}
	return best, nil
}

func RelativePath(root, candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("%w: a path is required", ErrInvalidSubject)
	}
	realRoot, err := vfs.ResolveRoot(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}
	real, err := vfs.Contain(root, filepath.FromSlash(candidate))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %s is not a path inside the workspace root", ErrInvalidSubject, candidate)
	}
	return filepath.ToSlash(rel), nil
}

func normalizeData(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(trimmed) > MaxDataBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrDataTooLarge, len(trimmed), MaxDataBytes)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, ErrInvalidData
	}
	return json.RawMessage(trimmed), nil
}

type pendingEvent struct {
	stored      *runtimetypes.Event
	coalesce    bool
	coalesceKey string
}

func (s *service) prepare(granted string, in Input) (pendingEvent, *Event, error) {
	eventType := strings.TrimSpace(in.Type)
	spec, ok := eventTypes[eventType]
	if !ok {
		return pendingEvent{}, nil, fmt.Errorf("%w: %q", ErrUnknownType, in.Type)
	}
	if strings.TrimSpace(in.Source) != spec.source {
		return pendingEvent{}, nil, fmt.Errorf("%w: %s is a %s event", ErrSourceMismatch, eventType, spec.source)
	}
	subject := strings.TrimSpace(in.Subject)
	if spec.pathSubject {
		var err error
		subject, err = RelativePath(granted, subject)
		if err != nil {
			return pendingEvent{}, nil, err
		}
	}
	if len(subject) > MaxSubjectBytes {
		return pendingEvent{}, nil, fmt.Errorf("%w: %d bytes, limit %d", ErrInvalidSubject, len(subject), MaxSubjectBytes)
	}
	data, err := normalizeData(in.Data)
	if err != nil {
		return pendingEvent{}, nil, err
	}

	scope := s.scope(granted)
	coalesceKey := scope + "\x00" + eventType + "\x00" + subject
	if spec.coalesce {
		s.mu.Lock()
		prev, seen := s.last[coalesceKey]
		s.mu.Unlock()
		if seen && s.now().Sub(prev.at) < coalesceWindow {
			return pendingEvent{}, &prev.event, nil
		}
	}
	return pendingEvent{
		stored: &runtimetypes.Event{
			WorkspaceID: scope,
			Type:        eventType,
			Source:      spec.source,
			Subject:     subject,
			Data:        data,
		},
		coalesce:    spec.coalesce,
		coalesceKey: coalesceKey,
	}, nil, nil
}

func (s *service) recorded(p pendingEvent) Event {
	event := fromStored(*p.stored)
	if p.coalesce {
		s.mu.Lock()
		s.last[p.coalesceKey] = coalesced{at: s.now(), event: event}
		s.mu.Unlock()
	}
	return event
}

func (s *service) Append(ctx context.Context, root string, in Input) (Event, error) {
	reportErr, _, end := s.tracker.Start(ctx, "append", "workspace_event", "type", in.Type, "root", root)
	defer end()
	granted, err := s.GrantedRoot(root)
	if err != nil {
		reportErr(err)
		return Event{}, err
	}
	p, hit, err := s.prepare(granted, in)
	if err != nil {
		reportErr(err)
		return Event{}, err
	}
	if hit != nil {
		return *hit, nil
	}
	if err := s.log.Append(ctx, p.stored); err != nil {
		reportErr(err)
		return Event{}, err
	}
	event := s.recorded(p)
	s.announce(ctx, granted, event)
	return event, nil
}

func (s *service) AppendBatch(ctx context.Context, root string, ins []Input) ([]Event, error) {
	reportErr, _, end := s.tracker.Start(ctx, "append_batch", "workspace_event", "count", len(ins), "root", root)
	defer end()
	out, err := s.appendBatch(ctx, root, ins)
	if err != nil {
		reportErr(err)
	}
	return out, err
}

func (s *service) appendBatch(ctx context.Context, root string, ins []Input) ([]Event, error) {
	granted, err := s.GrantedRoot(root)
	if err != nil {
		return nil, err
	}
	var rejected []error
	pending := make([]pendingEvent, 0, len(ins))
	stored := make([]*runtimetypes.Event, 0, len(ins))
	out := make([]Event, 0, len(ins))
	for _, in := range ins {
		p, hit, err := s.prepare(granted, in)
		switch {
		case err != nil:
			rejected = append(rejected, fmt.Errorf("%s %q: %w", in.Type, in.Subject, err))
		case hit != nil:
			out = append(out, *hit)
		default:
			pending = append(pending, p)
			stored = append(stored, p.stored)
		}
	}
	if len(stored) == 0 {
		return out, errors.Join(rejected...)
	}
	if err := s.log.AppendBatch(ctx, stored); err != nil {
		return out, errors.Join(append(rejected, err)...)
	}
	for _, p := range pending {
		out = append(out, s.recorded(p))
	}
	s.announce(ctx, granted, out[len(out)-1])
	return out, errors.Join(rejected...)
}

func (s *service) announce(ctx context.Context, root string, event Event) {
	if s.bus == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.bus.Publish(publishCtx, Subject(s.workspaceID, root), payload); err != nil {
		reportErr, _, end := s.tracker.Start(ctx, "announce", "workspace_event", "type", event.Type, "nid", event.NID)
		reportErr(fmt.Errorf("workspaceevents: announce failed; subscribers catch up from the log: %w", err))
		end()
	}
}

func (s *service) Since(ctx context.Context, root string, afterNID int64, limit int) ([]Event, error) {
	granted, err := s.GrantedRoot(root)
	if err != nil {
		return nil, err
	}
	if afterNID < 0 {
		afterNID = 0
	}
	rows, err := s.log.ListEventsSince(ctx, s.scope(granted), afterNID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromStored(row))
	}
	return out, nil
}

func (s *service) Latest(ctx context.Context, root string) (int64, error) {
	granted, err := s.GrantedRoot(root)
	if err != nil {
		return 0, err
	}
	rows, err := s.log.ListRecentEvents(ctx, s.scope(granted), 0, 1)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].NID, nil
}

func (s *service) Subscribe(ctx context.Context, root string, ch chan<- []byte) (libbus.Subscription, error) {
	granted, err := s.GrantedRoot(root)
	if err != nil {
		return nil, err
	}
	if s.bus == nil {
		return nil, errors.New("workspaceevents: no bus configured")
	}
	return s.bus.Stream(ctx, Subject(s.workspaceID, granted), ch)
}

func fromStored(row runtimetypes.Event) Event {
	return Event{
		NID:     row.NID,
		Type:    row.Type,
		Source:  row.Source,
		Subject: row.Subject,
		Time:    row.Time,
		Data:    row.Data,
		Hop:     row.Hop,
	}
}
