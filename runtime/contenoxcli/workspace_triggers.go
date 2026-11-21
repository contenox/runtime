package contenoxcli

import (
	"context"
	"encoding/json"
	"fmt"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/workspacetriggersapi"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/operatorinbox"
	"github.com/contenox/runtime/runtime/workflows"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/contenox/runtime/runtime/workspacetriggers"
)

type fleetStarter struct {
	fleet fleetservice.Service
}

func (s fleetStarter) StartMission(ctx context.Context, run workspacetriggers.Run) (string, error) {
	result, err := s.fleet.Dispatch(ctx, fleetservice.DispatchRequest{
		AgentName:      run.Trigger.AgentName,
		Intent:         run.Intent(),
		HITLPolicyName: run.Trigger.HITLPolicyName,
		Cwd:            run.Root,
	})
	if err != nil {
		return "", err
	}
	return result.MissionID, nil
}

func missionState(missions missionservice.Service) workspacetriggers.MissionState {
	return func(ctx context.Context, missionID string) (workspacetriggers.MissionOutcome, error) {
		m, err := missions.Get(ctx, missionID)
		if err != nil {
			return workspacetriggers.MissionOutcome{}, err
		}
		return workspacetriggers.MissionOutcome{
			Terminal: missionservice.IsTerminalStatus(m.Status),
			OK:       m.Status == missionservice.StatusLanded,
			Status:   string(m.Status),
		}, nil
	}
}

type policyChecker interface {
	ValidatePolicy(ctx context.Context, name string) error
}

func triggerCheck(agents agentregistryservice.Service, policies policyChecker) workspacetriggersapi.CheckFunc {
	return func(ctx context.Context, t workspacetriggers.Trigger) error {
		if t.Target == "workflow" {
			d, err := workflows.Load(t.Root, t.Workflow)
			if err != nil {
				return err
			}
			if d.Revision != t.WorkflowRevision {
				return fmt.Errorf("select the current workflow revision")
			}
			if policies != nil && t.HITLPolicyName != "" {
				return policies.ValidatePolicy(ctx, t.HITLPolicyName)
			}
			return nil
		}
		if t.Target == "notify" {
			return nil
		}
		if t.Target != "session" {
			if _, err := agentregistryservice.ResolveForSpawn(ctx, agents, t.AgentName); err != nil {
				return err
			}
		}
		if policies != nil {
			if err := policies.ValidatePolicy(ctx, t.HITLPolicyName); err != nil {
				return err
			}
		}
		return nil
	}
}

func newTriggerDispatcher(events workspaceevents.Service, log eventlog.Service, roots workspaceevents.Roots, db libdb.DBManager, workspaceID string, fleet fleetservice.Service, missions missionservice.Service, tracker libtracker.ActivityTracker, deliver func(context.Context, workspacetriggers.Run, string) error) *workspacetriggers.Dispatcher {
	return workspacetriggers.NewDispatcher(workspacetriggers.DispatcherConfig{
		WorkspaceID: workspaceID,
		DB:          db,
		Log:         log,
		Events:      events,
		Roots:       roots,
		Store:       workspacetriggers.Store{DB: db},
		Deliver:     deliver, Starter: fleetStarter{fleet: fleet},
		Missions: missionState(missions),
		Tracker:  tracker,
	})
}

func deliverWorkspaceEvent(wf *workflows.Service, operatorInbox operatorinbox.Service, sessionRouter *acpsvc.SessionRouter) func(context.Context, workspacetriggers.Run, string) error {
	return func(ctx context.Context, run workspacetriggers.Run, id string) error {
		if run.Trigger.Target == "workflow" {
			input, _ := json.Marshal(map[string]any{"event": run.Event, "paths": run.Paths})
			var nid int64
			origin := run.Event
			if evt := run.TopicEvent; evt != nil {
				nid = evt.NID
				input = evt.Data
				origin = evt.Subject
			}
			ctx = hitlservice.WithPolicyName(ctx, run.Trigger.HITLPolicyName)
			r, err := wf.Run(ctx, run.Root, workflows.Invocation{Name: run.Trigger.Workflow, Revision: run.Trigger.WorkflowRevision, ID: id, Origin: origin, Payload: input, EventNID: nid})
			if err == nil && r.Status == "failed" {
				err = fmt.Errorf("%s", r.Error)
			}
			return err
		}
		if run.Trigger.Target == "notify" {
			return operatorInbox.Add(ctx, &operatorinbox.Item{ID: id, Reason: operatorinbox.ReasonWorkspaceEvent, Intent: run.Intent(), Report: missionservice.Report{ID: id, Kind: missionservice.ReportKindFinding, Summary: run.Trigger.Name, Detail: run.Intent()}})
		}
		return sessionRouter.PromptWorkspaceEvent(ctx, run.Trigger.SessionID, run.Root, run.Trigger.SessionMode, run.Trigger.HITLPolicyName, run.Intent())
	}
}
