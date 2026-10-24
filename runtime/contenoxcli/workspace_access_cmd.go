package contenoxcli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/accessview"
	"github.com/spf13/cobra"
)

var workspaceAccessCmd = &cobra.Command{
	Use:   "access [--root <root>] [--policy <name>] <path> [<path>...]",
	Short: "Preview the HITL policy's read/write verdict for one or more paths.",
	Long: `Evaluate what the active human-in-the-loop policy would decide for a read and a
write access to each given path — without touching the filesystem, prompting for
approval, or running any tool call. This is the same policy evaluation the live
agent's tool dispatch gates run for local_fs (read_file/list_dir for the read
dimension, write_file for the write dimension), reached over a running
'contenox serve's REST API rather than the local database, since the verdict
depends on serve's own view of the workspace and its HITL policy engine.

Each <path> is relative to the workspace root being evaluated. A path that does
not resolve inside that root (outside every allowlisted root, or a symlink whose
real target escapes it) comes back reachable:false with no read/write verdict —
no policy is evaluated for a path that is not really inside the root.

  contenox workspace access src/main.go
  contenox workspace access --policy hitl-policy-strict.json .ssh/id_rsa
  contenox workspace access --root /home/me/project src/main.go .ssh/id_rsa ../escape

By default this talks to http://127.0.0.1:32123, overridable with --server/--token
or the CONTENOX_SERVER_URL/CONTENOX_SERVER_TOKEN environment variables — see
'contenox approvals' for the same convention.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runWorkspaceAccess,
}

func init() {
	addServeClientFlags(workspaceAccessCmd)
	workspaceAccessCmd.Flags().String("root", "",
		"Workspace root the paths resolve against (a granted root, or a directory under one); omitted resolves to serve's default (first-configured) root")
	workspaceAccessCmd.Flags().String("policy", "",
		"HITL policy file name to evaluate against, e.g. hitl-policy-strict.json (see 'contenox config get hitl-policy-name'); omitted uses the runtime's active policy resolution, the same one the live agent uses")

	workspaceCmd.AddCommand(workspaceAccessCmd)
}

type accessEvaluateRequest struct {
	Paths []string `json:"paths"`
}

type accessEvaluateResponse struct {
	PolicyName string                   `json:"policyName"`
	Verdicts   []accessview.PathVerdict `json:"verdicts"`
}

func (c *serveClient) evaluateWorkspaceAccess(ctx context.Context, root, policy string, paths []string) (accessEvaluateResponse, error) {
	q := url.Values{}
	if strings.TrimSpace(root) != "" {
		q.Set("root", root)
	}
	if strings.TrimSpace(policy) != "" {
		q.Set("policy", policy)
	}
	path := "/workspace/access"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	var out accessEvaluateResponse
	if err := c.post(ctx, path, accessEvaluateRequest{Paths: paths}, &out); err != nil {
		return accessEvaluateResponse{}, err
	}
	return out, nil
}

func runWorkspaceAccess(cmd *cobra.Command, args []string) error {
	ctx := libtracker.WithNewRequestID(context.Background())

	client, err := newServeClient(cmd)
	if err != nil {
		return err
	}

	root, _ := cmd.Flags().GetString("root")
	policy, _ := cmd.Flags().GetString("policy")

	resp, err := client.evaluateWorkspaceAccess(ctx, root, policy, args)
	if err != nil {
		return fmt.Errorf("evaluate workspace access: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Policy: %s\n", stringOrDash(resp.PolicyName))

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tREACHABLE\tREAD\tWRITE\tREASON")
	for _, v := range resp.Verdicts {
		fmt.Fprintf(w, "%s\t%t\t%s\t%s\t%s\n",
			v.Path,
			v.Reachable,
			dimensionAction(v.Read),
			dimensionAction(v.Write),
			accessReasonColumn(v.Read, v.Write),
		)
	}
	return w.Flush()
}

func dimensionAction(dv *accessview.DimensionVerdict) string {
	if dv == nil {
		return "-"
	}
	return dv.Action
}

func accessReasonColumn(read, write *accessview.DimensionVerdict) string {
	var parts []string
	if f := dimensionReasonFragment("read", read); f != "" {
		parts = append(parts, f)
	}
	if f := dimensionReasonFragment("write", write); f != "" {
		parts = append(parts, f)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func dimensionReasonFragment(label string, dv *accessview.DimensionVerdict) string {
	if dv == nil || dv.Action == "allow" {
		return ""
	}
	reason := dv.Reason
	if dv.Reason == "matched_rule" && dv.Rule != nil {
		reason = fmt.Sprintf("matched_rule#%d", *dv.Rule)
	}
	return label + ":" + reason
}
