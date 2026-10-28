package contenoxcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/spf13/cobra"
)

var approvalsCmd = &cobra.Command{
	Use:   "approvals",
	Short: "Read and answer pending human-in-the-loop approvals.",
	Long: `List and answer the human-in-the-loop approvals a running 'contenox-runtime serve'
is holding pending — the inbox for asks raised by an agent working with no
attached session (dispatched fleet work, a headless API caller). A permission
request that would otherwise hang until its policy-rule timeout, or the
serve-level ceiling, is answerable here as soon as it lands.

Unlike 'contenox-runtime state'/'session', which open the local database directly,
a pending approval is a goroutine parked inside a running 'contenox-runtime serve'
process — answering it has to reach that process, not just its database. So
this command talks to serve's REST API over HTTP instead: by default
http://127.0.0.1:32123, overridable with --server/--token or the
CONTENOX_SERVER_URL/CONTENOX_SERVER_TOKEN environment variables.

Examples:
  contenox approvals list
  contenox approvals list --json
  contenox approvals answer 3f9c6e2a-1b4d --approve
  contenox approvals answer 3f9c6e2a-1b4d --deny`,
}

var approvalsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending approvals, newest first.",
	Long: `Fetch the pending human-in-the-loop approvals from a running 'contenox-runtime serve'
and print them as a table of id, tool, args summary, policy, matched rule,
diff presence, the agent/mission/instance/session the ask came from, and the
created/expires timestamps — everything an operator needs to decide, and to
always be able to name both which policy rule escalated the ask and which unit
it is holding up. The attribution columns are empty ("-") for an ask raised
outside the fleet, and the mission column is empty for an unattended session
that is not on a mission.`,
	Args: cobra.NoArgs,
	RunE: runApprovalsList,
}

var approvalsAnswerCmd = &cobra.Command{
	Use:   "answer <id> --approve|--deny|--reply <text>",
	Short: "Approve or deny a pending approval, or reply to a unit's question.",
	Long: `Answer one pending ask by id (see 'contenox-runtime approvals list').

Two kinds of ask arrive in this queue, and they are answered differently:

  a PERMISSION ask  — a gated tool call waiting on a verdict. Answer it with
                      exactly one of --approve/--deny.
  an ATTENTION ask  — a running mission unit's QUESTION ("which project did
                      you mean?"), listed as mission.mission_ask_attention.
                      Answer it with --reply "<your answer>": the text is
                      handed straight back to the unit as the result of the
                      tool call it is parked on, and it continues with it on
                      the same turn.

--reply is refused on a permission ask (prose is not a verdict), and
--approve/--deny on an attention ask leaves the unit with no answer, so it
falls back to filing your question as a blocker.

An id that is unknown, already answered, or expired (past its policy's
timeout and auto-resolved by serve's sweeper) fails with a non-zero exit
status describing which of those it was — answering twice is never silently
a no-op.`,
	Args: cobra.ExactArgs(1),
	RunE: runApprovalsAnswer,
}

func init() {
	addServeClientFlags(approvalsCmd)

	approvalsListCmd.Flags().Int("limit", 100, "Maximum number of pending approvals to list (0 = server default cap)")
	approvalsListCmd.Flags().Bool("json", false, "Print the raw records as JSON instead of a table")

	approvalsAnswerCmd.Flags().Bool("approve", false, "Approve the pending ask (permission asks)")
	approvalsAnswerCmd.Flags().Bool("deny", false, "Deny the pending ask (permission asks)")
	approvalsAnswerCmd.Flags().String("reply", "", "Reply to a unit's question with this text (attention asks) — the unit receives it as its tool result")
	approvalsAnswerCmd.Flags().Bool("json", false, "Print the result as JSON")
	approvalsAnswerCmd.MarkFlagsMutuallyExclusive("approve", "deny")
	approvalsAnswerCmd.MarkFlagsMutuallyExclusive("approve", "reply")
	approvalsAnswerCmd.MarkFlagsMutuallyExclusive("deny", "reply")
	approvalsAnswerCmd.MarkFlagsOneRequired("approve", "deny", "reply")

	approvalsCmd.AddCommand(approvalsListCmd)
	approvalsCmd.AddCommand(approvalsAnswerCmd)
}

func (c *serveClient) listPendingApprovals(ctx context.Context, limit int) ([]*runtimetypes.HITLApproval, error) {
	path := "/approvals?limit=" + strconv.Itoa(limit)
	var out []*runtimetypes.HITLApproval
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*runtimetypes.HITLApproval{}
	}
	return out, nil
}

type answerApprovalRequest struct {
	Approved bool   `json:"approved"`
	Answer   string `json:"answer,omitempty"`
}

func (c *serveClient) answerApproval(ctx context.Context, id string, approved bool) error {
	return c.post(ctx, "/approvals/"+url.PathEscape(id), answerApprovalRequest{Approved: approved}, nil)
}

func (c *serveClient) replyToAsk(ctx context.Context, id, text string) error {
	return c.post(ctx, "/approvals/"+url.PathEscape(id), answerApprovalRequest{Answer: text}, nil)
}

func runApprovalsList(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	limit, _ := cmd.Flags().GetInt("limit")
	approvals, err := client.listPendingApprovals(ctx, limit)
	if err != nil {
		return fmt.Errorf("list approvals: %w", err)
	}

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(approvals)
	}

	if len(approvals) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no pending approvals)")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTOOL\tARGS\tPOLICY\tRULE\tDIFF\tAGENT\tMISSION\tINSTANCE\tSESSION\tCREATED\tEXPIRES")
	for _, a := range approvals {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			a.ID,
			a.ToolsName+"."+a.ToolName,
			stringOrDash(a.ArgsSummary),
			stringOrDash(a.PolicyName),
			intPtrOrDash(a.MatchedRule),
			approvalDiffColumn(a.Diff),
			stringOrDash(a.AgentName),
			stringPtrOrDash(a.MissionID),
			stringOrDash(a.InstanceID),
			stringOrDash(a.SessionID),
			formatApprovalTime(a.CreatedAt),
			formatApprovalTime(a.ExpiresAt),
		)
	}
	return w.Flush()
}

func runApprovalsAnswer(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())
	id := args[0]

	approved, _ := cmd.Flags().GetBool("approve")
	reply, _ := cmd.Flags().GetString("reply")
	asJSON, _ := cmd.Flags().GetBool("json")

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(reply) != "" {
		if err := client.replyToAsk(ctx, id, reply); err != nil {
			return fmt.Errorf("reply to ask %q: %w", id, err)
		}
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"id": id, "answer": reply})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Replied to ask %s. The unit has your answer.\n", id)
		return nil
	}

	if err := client.answerApproval(ctx, id, approved); err != nil {
		return fmt.Errorf("answer approval %q: %w", id, err)
	}

	verb := "denied"
	if approved {
		verb = "approved"
	}

	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"id": id, "approved": approved})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Approval %s %s.\n", id, verb)
	return nil
}

func stringOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func stringPtrOrDash(v *string) string {
	if v == nil {
		return "-"
	}
	return stringOrDash(*v)
}

func intPtrOrDash(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

func approvalDiffColumn(diff *string) string {
	if diff == nil || strings.TrimSpace(*diff) == "" {
		return "-"
	}
	lines := strings.Count(*diff, "\n") + 1
	return fmt.Sprintf("yes (%d lines)", lines)
}

func formatApprovalTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
