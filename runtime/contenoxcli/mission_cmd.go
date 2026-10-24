package contenoxcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/spf13/cobra"
)

var missionCmd = &cobra.Command{
	Use:   "mission",
	Short: "Fire and track missions: the headless way to work with an agent.",
	Long: `Work with an agent in MISSION mode — the dual of chat mode. In chat you
prompt an agent turn by turn and you are the one approving each gated action. In
mission mode you fire a one-line intent at a declared agent under an ENVELOPE (a
HITL policy that bounds what it may do unattended) and detach; the unit acts
inside the envelope and only crossing it costs your attention, in the approvals
inbox ('contenox approvals').

A mission is a subagent of whatever process fires it: /mission in an editor
session ('contenox acp') dispatches in-process and its reports arrive live back
in that session. THESE verbs are the operator's remote lever — they fire onto,
and read from, a running 'contenox serve' over its REST API (default
http://127.0.0.1:32123; override with --server/--token or
CONTENOX_SERVER_URL/CONTENOX_SERVER_TOKEN).

  contenox mission fire --agent reviewer --intent "triage the failing CI run" --policy hitl-policy-strict.json
  contenox mission fire --intent "summarise today's commits"   # --agent/--policy from config defaults
  contenox mission list                     # what is running, for whom, and why
  contenox mission show <mission-id>        # the mission plus its reports, newest first

Set the defaults 'mission fire' falls back to with:
  contenox config set default-mission-agent  <agent-name>
  contenox config set default-mission-policy <hitl-policy-file>`,
}

var missionFireCmd = &cobra.Command{
	Use:   "fire",
	Short: "Fire a mission: dispatch an agent at an intent under an envelope.",
	Long: `Fire a mission — bring up a unit for a declared agent, hand it a one-line
intent as its first turn, and detach. The unit runs unattended inside the
envelope (--policy): a HITL policy that decides, per gated action, whether the
unit may proceed on its own or must escalate to the approvals inbox.

  contenox mission fire --agent reviewer --intent "triage the failing CI run" --policy hitl-policy-strict.json
  contenox mission fire --intent "summarise today's commits"

--intent is always required. --agent and --policy fall back to the config keys
default-mission-agent / default-mission-policy when omitted, so a configured
operator can fire with intent alone. A mission with no agent or no envelope is
refused — a mission must name both what runs and what bounds it.

--cwd roots the unit's session in a directory; it must be absolute and inside a
workspace root serve allows. Omitted, it defaults to serve's project root.

By default firing returns as soon as the unit's session is open (the first turn
runs detached), printing the mission/instance/session ids to follow it with
'contenox mission show' and 'contenox fleet show'. The mission id is
machine-readable on stdout — bare with -q, or inside the --json object — so a
script can capture and correlate it.

Scripting a sequence of missions:

  # -q prints only the mission id; --wait blocks until the fired unit terminates.
  contenox mission fire -q --wait --intent "step one" && \
    contenox mission fire -q --wait --intent "step two"

--wait blocks until the fired unit reaches an HONESTLY observable terminal
condition, so 'fire --wait && fire --wait' composes in a shell. It prefers the
mission's terminal Status when one is set, and falls back to reports and instance
state for a mission that ends without one. The exit code each maps to:

  0  the mission LANDED, or (no status yet) the unit filed a RESULT report — a
     completed mission.
  1  the mission DERAILED, was STUCK or ABANDONED; or (no status) the unit
     stopped, vanished, or ended in state error/warning without a result — a
     failed or gone dispatch.
  2  the unit filed a BLOCKER report — it needs your attention.
  3  --wait-timeout elapsed with the unit still running and no verdict — an
     honest "indeterminate", not a success. A mission that only files progress or
     finding reports (never a result or a terminal status) ends here: those are
     intermediate signals, not an outcome, so --wait keeps waiting through them.
  130  interrupted (Ctrl-C).

Mission Status is the authoritative signal: a unit (once the plan/status tooling
is wired) marks its own mission landed/derailed/stuck via missionservice.Finish,
and --wait maps that terminal state directly. The report and instance-state
checks remain as fallbacks, because a mission can still end without a status — an
external ACP unit that is torn down or crashes never calls Finish — and an honest
"it stopped, outcome unknown" beats hanging until the deadline. STUCK shares exit
code 1 with DERAILED: the two carry different status (preserved in the record and
the inbox) but the same coarse shell verdict, "ended without success".`,
	Args: cobra.NoArgs,
	RunE: runMissionFire,
}

var missionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List missions, newest first.",
	Long: `Print the missions a running 'contenox serve' holds, newest first: each
mission's id, status, agent, envelope, when it last changed, and the intent it
was fired with. --json emits the raw records (including the session/instance ids
and the parent-session supervision edge).`,
	Args: cobra.NoArgs,
	RunE: runMissionList,
}

var missionShowCmd = &cobra.Command{
	Use:   "show <mission-id>",
	Short: "Show one mission and its reports.",
	Long: `Print one mission's full record — intent, agent, envelope, status, the
session/instance it spawned, the parent session that supervises it (when fired
from a chat by /mission rather than by an operator directly), liveness
(last heartbeat / last error) — followed by the reports the unit filed back,
newest first. An unknown mission id fails with a non-zero exit status. --json
emits {mission, reports} as one object.`,
	Args: cobra.ExactArgs(1),
	RunE: runMissionShow,
}

func init() {
	addServeClientFlags(missionCmd)

	missionFireCmd.Flags().String("agent", "", "Declared agent to fire (default: config default-mission-agent)")
	missionFireCmd.Flags().String("intent", "", "One-line mission intent — what the unit is sent to do (required)")
	missionFireCmd.Flags().String("policy", "", "Envelope: the HITL policy that bounds the unit (default: config default-mission-policy)")
	missionFireCmd.Flags().String("cwd", "", "Absolute working directory for the unit's session (default: serve's project root)")
	missionFireCmd.Flags().Bool("json", false, "Print the dispatch result (ids) as JSON")
	missionFireCmd.Flags().BoolP("quiet", "q", false, "Print only the mission id to stdout (for capture in scripts)")
	missionFireCmd.Flags().Bool("wait", false, "Block until the fired unit reaches an observable terminal condition; exit code reflects the outcome (see 'contenox mission fire --help')")
	missionFireCmd.Flags().Duration("wait-timeout", 5*time.Minute, "With --wait, the ceiling before giving up (exit 3)")
	missionFireCmd.Flags().Duration("wait-interval", 2*time.Second, "With --wait, how often to poll for a terminal condition")

	missionListCmd.Flags().Int("limit", 100, "Maximum number of missions to list")
	missionListCmd.Flags().Bool("json", false, "Print the raw mission records as JSON instead of a table")

	missionShowCmd.Flags().Int("limit", 100, "Maximum number of reports to show")
	missionShowCmd.Flags().Bool("json", false, "Print {mission, reports} as JSON instead of a summary")

	missionCmd.AddCommand(missionFireCmd)
	missionCmd.AddCommand(missionListCmd)
	missionCmd.AddCommand(missionShowCmd)
}

func (c *serveClient) dispatchMission(ctx context.Context, req fleetservice.DispatchRequest) (fleetservice.DispatchResult, error) {
	var out fleetservice.DispatchResult
	if err := c.post(ctx, "/fleet/dispatch", req, &out); err != nil {
		return fleetservice.DispatchResult{}, err
	}
	return out, nil
}

func (c *serveClient) listMissions(ctx context.Context, limit int) ([]*missionservice.Mission, error) {
	path := "/missions"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []*missionservice.Mission
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*missionservice.Mission{}
	}
	return out, nil
}

func (c *serveClient) getMission(ctx context.Context, id string) (*missionservice.Mission, error) {
	var out missionservice.Mission
	if err := c.get(ctx, "/missions/"+url.PathEscape(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *serveClient) listMissionReports(ctx context.Context, id string, limit int) ([]*missionservice.Report, error) {
	path := "/missions/" + url.PathEscape(id) + "/reports"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []*missionservice.Report
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*missionservice.Report{}
	}
	return out, nil
}

func runMissionFire(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())

	agent, _ := cmd.Flags().GetString("agent")
	intent, _ := cmd.Flags().GetString("intent")
	policy, _ := cmd.Flags().GetString("policy")
	cwd, _ := cmd.Flags().GetString("cwd")

	if strings.TrimSpace(intent) == "" {
		return fmt.Errorf("--intent is required: a mission must name what the unit is sent to do")
	}

	agent, policy = resolveMissionFireDefaults(cmd, agent, policy)

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	result, err := client.dispatchMission(ctx, fleetservice.DispatchRequest{
		AgentName:      strings.TrimSpace(agent),
		Intent:         intent,
		HITLPolicyName: strings.TrimSpace(policy),
		Cwd:            strings.TrimSpace(cwd),
	})
	if err != nil {
		return fmt.Errorf("fire mission: %w", err)
	}

	wait, _ := cmd.Flags().GetBool("wait")
	asJSON, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	switch {
	case asJSON:
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	case quiet:
		fmt.Fprintln(cmd.OutOrStdout(), result.MissionID)
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "Mission fired.\n")
		fmt.Fprintf(w, "Mission:\t%s\n", result.MissionID)
		fmt.Fprintf(w, "Instance:\t%s\n", result.InstanceID)
		fmt.Fprintf(w, "Session:\t%s\n", result.SessionID)
		if err := w.Flush(); err != nil {
			return err
		}
		if !wait {
			fmt.Fprintf(cmd.OutOrStdout(), "Track it with: contenox mission show %s\n", result.MissionID)
		}
	}

	if !wait {
		return nil
	}

	timeout, _ := cmd.Flags().GetDuration("wait-timeout")
	interval, _ := cmd.Flags().GetDuration("wait-interval")
	waitCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	code, msg := waitForMissionOutcome(waitCtx, client, result, timeout, interval, cmd.ErrOrStderr())
	fmt.Fprintln(cmd.ErrOrStderr(), msg)
	if code != 0 {
		return &exitError{code}
	}
	return nil
}

const (
	missionWaitReported = 0
	missionWaitStopped  = 1
	missionWaitBlocked  = 2
	missionWaitTimeout  = 3
	missionWaitAborted  = 130
)

func waitForMissionOutcome(
	ctx context.Context,
	client *serveClient,
	result fleetservice.DispatchResult,
	timeout, interval time.Duration,
	progress io.Writer,
) (int, string) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	fmt.Fprintf(progress, "Waiting for mission %s (unit %s), up to %s…\n", result.MissionID, result.InstanceID, timeout)

	for {
		if result.MissionID != "" {
			if m, err := client.getMission(ctx, result.MissionID); err == nil {
				if code, msg, done := verdictFromStatus(m); done {
					return code, msg
				}
			}
		}

		if result.MissionID != "" {
			if reports, err := client.listMissionReports(ctx, result.MissionID, 100); err == nil && len(reports) > 0 {
				latest := reports[0]
				switch latest.Kind {
				case missionservice.ReportKindBlocker:
					return missionWaitBlocked, fmt.Sprintf("mission %s is BLOCKED: %s — needs attention (see 'contenox mission show %s').",
						result.MissionID, latest.Summary, result.MissionID)
				case missionservice.ReportKindResult:
					return missionWaitReported, fmt.Sprintf("mission %s produced a result: %s", result.MissionID, latest.Summary)
				}
			}
		}

		if result.InstanceID != "" {
			status, err := client.getInstance(ctx, result.InstanceID)
			if err != nil {
				var se *ServeError
				if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
					return missionWaitStopped, fmt.Sprintf("unit %s is gone (torn down) and filed no report.", result.InstanceID)
				}
			} else {
				switch status.State {
				case agentinstance.StateError, agentinstance.StateWarning:
					return missionWaitStopped, fmt.Sprintf("unit %s ended in state %q and filed no report.", result.InstanceID, status.State)
				case agentinstance.StateStopped:
					return missionWaitStopped, fmt.Sprintf("unit %s stopped and filed no report.", result.InstanceID)
				}
			}
		}

		if time.Now().After(deadline) {
			return missionWaitTimeout, fmt.Sprintf("timed out after %s: mission %s still running with no report (indeterminate).", timeout, result.MissionID)
		}

		select {
		case <-ctx.Done():
			return missionWaitAborted, fmt.Sprintf("interrupted; mission %s keeps running server-side.", result.MissionID)
		case <-time.After(interval):
		}
	}
}

func verdictFromStatus(m *missionservice.Mission) (int, string, bool) {
	reason := ""
	if strings.TrimSpace(m.StatusReason) != "" {
		reason = ": " + m.StatusReason
	}
	switch m.Status {
	case missionservice.StatusLanded:
		return missionWaitReported, fmt.Sprintf("mission %s LANDED%s", m.ID, reason), true
	case missionservice.StatusDerailed:
		return missionWaitStopped, fmt.Sprintf("mission %s DERAILED%s", m.ID, reason), true
	case missionservice.StatusStuck:
		return missionWaitStopped, fmt.Sprintf("mission %s is STUCK%s — needs attention (see 'contenox mission show %s').", m.ID, reason, m.ID), true
	case missionservice.StatusAbandoned:
		return missionWaitStopped, fmt.Sprintf("mission %s was ABANDONED%s", m.ID, reason), true
	default:
		return 0, "", false
	}
}

func resolveMissionFireDefaults(cmd *cobra.Command, agent, policy string) (string, string) {
	if strings.TrimSpace(agent) != "" && strings.TrimSpace(policy) != "" {
		return agent, policy
	}
	db, store, err := openConfigDB(cmd)
	if err != nil {
		return agent, policy
	}
	defer db.Close()
	ctx := libtracker.WithNewRequestID(context.Background())
	if strings.TrimSpace(agent) == "" {
		agent = clikv.Read(ctx, store, "default-mission-agent")
	}
	if strings.TrimSpace(policy) == "" {
		policy = clikv.Read(ctx, store, "default-mission-policy")
	}
	return agent, policy
}

func runMissionList(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	limit, _ := cmd.Flags().GetInt("limit")
	missions, err := client.listMissions(ctx, limit)
	if err != nil {
		return fmt.Errorf("list missions: %w", err)
	}

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(missions)
	}

	if len(missions) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no missions)")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tAGENT\tENVELOPE\tUPDATED\tINTENT")
	for _, m := range missions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID,
			stringOrDash(string(m.Status)),
			stringOrDash(m.AgentName),
			stringOrDash(m.HITLPolicyName),
			formatApprovalTime(m.UpdatedAt),
			stringOrDash(m.Intent),
		)
	}
	return w.Flush()
}

type missionShowPayload struct {
	Mission *missionservice.Mission  `json:"mission"`
	Reports []*missionservice.Report `json:"reports"`
}

func runMissionShow(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())
	id := args[0]

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	mission, err := client.getMission(ctx, id)
	if err != nil {
		return fmt.Errorf("show mission %q: %w", id, err)
	}

	limit, _ := cmd.Flags().GetInt("limit")
	reports, err := client.listMissionReports(ctx, id, limit)
	if err != nil {
		return fmt.Errorf("show mission %q reports: %w", id, err)
	}

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(missionShowPayload{Mission: mission, Reports: reports})
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Mission:\t%s\n", mission.ID)
	fmt.Fprintf(w, "Status:\t%s\n", stringOrDash(string(mission.Status)))
	fmt.Fprintf(w, "Agent:\t%s\n", stringOrDash(mission.AgentName))
	fmt.Fprintf(w, "Envelope:\t%s\n", stringOrDash(mission.HITLPolicyName))
	fmt.Fprintf(w, "Intent:\t%s\n", stringOrDash(mission.Intent))
	fmt.Fprintf(w, "Instance:\t%s\n", stringOrDash(mission.InstanceID))
	fmt.Fprintf(w, "Session:\t%s\n", stringOrDash(mission.SessionID))
	fmt.Fprintf(w, "Parent session:\t%s\n", stringOrDash(mission.ParentSessionID))
	if mission.LastHeartbeat != nil {
		fmt.Fprintf(w, "Last heartbeat:\t%s\n", formatApprovalTime(*mission.LastHeartbeat))
	} else {
		fmt.Fprintf(w, "Last heartbeat:\t-\n")
	}
	fmt.Fprintf(w, "Last error:\t%s\n", stringOrDash(mission.LastError))
	fmt.Fprintf(w, "Created:\t%s\n", formatApprovalTime(mission.CreatedAt))
	fmt.Fprintf(w, "Updated:\t%s\n", formatApprovalTime(mission.UpdatedAt))
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout())
	if len(reports) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Reports: (none)")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Reports (%d, newest first):\n", len(reports))
	rw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(rw, "  KIND\tWHEN\tSUMMARY")
	for _, r := range reports {
		fmt.Fprintf(rw, "  %s\t%s\t%s\n",
			stringOrDash(string(r.Kind)),
			formatApprovalTime(r.CreatedAt),
			stringOrDash(r.Summary),
		)
	}
	return rw.Flush()
}
