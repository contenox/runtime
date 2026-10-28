package contenoxcli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type missionSupervision struct {
	missions missionservice.Service
	hitl     hitlservice.Service
}

var (
	_ missiontools.SupervisorStore   = missionSupervision{}
	_ missiontools.AttentionResolver = missionSupervision{}
)

const supervisorScanPage = 200

func (s missionSupervision) MissionsFiredBy(ctx context.Context, parentSessionID string, limit int) ([]*missionservice.Mission, error) {
	if parentSessionID == "" {
		return nil, nil
	}
	page, err := s.missions.List(ctx, nil, supervisorScanPage)
	if err != nil {
		return nil, err
	}
	out := make([]*missionservice.Mission, 0, 8)
	for _, m := range page {
		if m == nil || m.ParentSessionID != parentSessionID {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s missionSupervision) ListReports(ctx context.Context, missionID string, limit int) ([]*missionservice.Report, error) {
	return s.missions.ListReports(ctx, missionID, limit)
}

func (s missionSupervision) PendingAsks(ctx context.Context, missionID string) ([]missiontools.PendingAsk, error) {
	rows, err := s.hitl.PendingAttentionAsks(ctx, missionID)
	if err != nil {
		return nil, err
	}
	out := make([]missiontools.PendingAsk, 0, len(rows))
	for _, row := range rows {
		ask := missiontools.PendingAsk{
			AskID:     row.ID,
			MissionID: missionID,
			Question:  row.ArgsSummary,
			AskedAt:   row.CreatedAt.UTC().Format(time.RFC3339),
		}
		if row.Diff != nil {
			ask.Detail = *row.Diff
		}
		out = append(out, ask)
	}
	return out, nil
}

func (s missionSupervision) AnswerAsAgent(ctx context.Context, askID, text string) error {
	return s.hitl.AnswerAsAgent(ctx, askID, text)
}

type fleetHolder struct {
	mu         sync.Mutex
	dispatcher acpsvc.MissionDispatcher
}

func (h *fleetHolder) set(dispatcher acpsvc.MissionDispatcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dispatcher = dispatcher
}

func (h *fleetHolder) get() acpsvc.MissionDispatcher {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dispatcher
}

type fleetSpawner struct {
	fleet func() acpsvc.MissionDispatcher
}

func (f fleetSpawner) Spawn(ctx context.Context, spec missiontools.SubagentSpec) (missiontools.SubagentHandle, error) {
	if f.fleet == nil {
		return missiontools.SubagentHandle{}, fmt.Errorf("the fleet is not running in this process")
	}
	fleet := f.fleet()
	if fleet == nil {
		return missiontools.SubagentHandle{}, fmt.Errorf("the fleet is not running in this process")
	}
	res, err := fleet.Dispatch(ctx, fleetservice.DispatchRequest{
		AgentName: spec.AgentName, Intent: spec.Intent, HITLPolicyName: spec.HITLPolicyName, ParentSessionID: spec.ParentSessionID,
	})
	if err != nil {
		return missiontools.SubagentHandle{}, err
	}
	return missiontools.SubagentHandle{MissionID: res.MissionID}, nil
}

func subagentDefaults(db libdb.DBManager) missiontools.SubagentDefaults {
	return func(ctx context.Context) (string, string) {
		store := runtimetypes.New(db.WithoutTransaction())
		return strings.TrimSpace(clikv.Read(ctx, store, "default-mission-agent")),
			strings.TrimSpace(clikv.Read(ctx, store, "default-mission-policy"))
	}
}
