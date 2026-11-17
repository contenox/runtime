package contenoxcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/spf13/cobra"
)

var modelPullCmd = &cobra.Command{
	Use:   "pull <name>",
	Short: "Download a complete native model bundle.",
	Long:  modelacquisition.Contract.Operations[1].Description,
	Args:  cobra.ExactArgs(1),
	RunE:  runModelPull,
}

var modelRegistryListCmd = &cobra.Command{
	Use:   "registry-list",
	Short: "List downloadable native models.",
	Long:  modelacquisition.Contract.Operations[0].Description,
	Args:  cobra.NoArgs,
	RunE:  runModelRegistryList,
}

func runModelRegistryList(cmd *cobra.Command, _ []string) error {
	models, err := modelregistry.NewCurated().List(cmd.Context())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tENGINE\tBYTES\tDESCRIPTION")
	for _, m := range models {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", m.Name, m.BackendType(), m.SizeBytes, m.Label())
	}
	return w.Flush()
}

func runModelPull(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	db, _, err := openConfigDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()
	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer bus.Close()
	tracker := libtracker.NoopTracker{}
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels(), runtimestate.WithTracker(tracker))
	if err != nil {
		return err
	}
	setup, _, closeSetup, err := inferenceSetupFactory(db, bus, nil, state, tracker, "")(ctx)
	if err != nil {
		return err
	}
	defer closeSetup()
	dataRoot := modeldconn.DataRootFromContext(ctx)
	service, err := modelacquisition.New(db, setup, state, modelregistry.NewCurated(), modelacquisition.StoreInstaller{}, dataRoot, tracker)
	if err != nil {
		return err
	}
	return pullModel(ctx, service, args[0], dataRoot, cmd.OutOrStdout())
}

func pullModel(ctx context.Context, service *modelacquisition.Service, name, dataRoot string, out io.Writer) error {
	err := service.Pull(ctx, name, func(progress modelacquisition.DownloadProgress) error {
		if progress.Total > 0 {
			_, err := fmt.Fprintf(out, "\rDownloading %s: %s / %s", name, modelstore.FormatSize(progress.Downloaded), modelstore.FormatSize(progress.Total))
			return err
		}
		_, err := fmt.Fprintf(out, "\rDownloading %s: %s", name, modelstore.FormatSize(progress.Downloaded))
		return err
	})
	var unknown *modelacquisition.UnknownModelError
	if errors.As(err, &unknown) {
		return fmt.Errorf("%q is not a downloadable model; 'contenox-runtime model registry-list' lists the models", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nModel %s available in %s\n", name, modelstore.Dir(dataRoot, ""))
	return nil
}

func promptModeldModel(ctx context.Context, out io.Writer, scanner *bufio.Scanner, defaultModel string) string {
	root := modeldconn.DataRootFromContext(ctx)
	models, err := modelstore.NewAdmin(modelstore.Dir(root, "")).ListModels(ctx)
	if err == nil && len(models) > 0 {
		names := make([]string, 0, len(models))
		for _, m := range models {
			names = append(names, m.Name)
		}
		return promptNativeModelMenu(out, scanner, names, preselectNativeModel(names, defaultModel))
	}
	fmt.Fprintf(out, "  No native models found in %s.\n", modelstore.Dir(root, ""))
	fmt.Fprintf(out, "  Download one with: contenox-runtime model pull %s\n", defaultModel)
	return promptLine(out, scanner, fmt.Sprintf("  Model [%s]", defaultModel), defaultModel)
}

func init() {
	modelCmd.AddCommand(modelPullCmd, modelRegistryListCmd, newNativeShowCmd(), newNativePSCmd(), newNativeStopCmd())
	modelListCmd.Flags().Bool("local", false, "List installed native models without contacting a worker.")
	for _, cmd := range []*cobra.Command{
		{Use: modelPullCmd.Use, Short: modelPullCmd.Short, Long: modelPullCmd.Long, Args: modelPullCmd.Args, RunE: modelPullCmd.RunE},
		newNativeListCmd(), newNativeShowCmd(), newNativePSCmd(), newNativeStopCmd(),
	} {
		reservedSubcommands[cmd.Name()] = true
		for _, alias := range cmd.Aliases {
			reservedSubcommands[alias] = true
		}
		rootCmd.AddCommand(cmd)
	}
}

func newNativeListCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List installed native models without starting a worker.", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return printNativeModels(cmd) }}
	cmd.Flags().Bool("json", false, "Print installed model metadata as JSON.")
	return cmd
}

func printNativeModels(cmd *cobra.Command) error {
	models, err := modelstore.NewAdmin(modelstore.Dir(modeldconn.DataRootFromContext(cmd.Context()), "")).ListModels(cmd.Context())
	if err != nil {
		return err
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	if models == nil {
		models = []modelstore.NodeModel{}
	}
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(models)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tENGINE\tBYTES\tMODEL CONTEXT")
	for _, m := range models {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\n", m.Name, m.Type, m.SizeBytes, m.ContextLength)
	}
	return w.Flush()
}

func newNativeShowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "show <model>", Short: "Show a native model's worker-resolved capabilities and capacity.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			models, err := modelstore.NewAdmin(modelstore.Dir(modeldconn.DataRootFromContext(cmd.Context()), "")).ListModels(cmd.Context())
			if err != nil {
				return err
			}
			ref := modeldconn.ModelRef{Name: args[0]}
			for _, model := range models {
				if model.Name == ref.Name {
					ref.Type, ref.Digest = model.Type, model.Digest
					break
				}
			}
			if ref.Type == "" {
				return fmt.Errorf("native model %q is not installed; use 'contenox-runtime pull <name>'", args[0])
			}
			info, err := modeldconn.Describe(cmd.Context(), ref, transport.Config{})
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "Model\t%s\nRuntime\t%s\nDevice\t%s %s\nModel context\t%d\nEffective context\t%d\nTools\t%t\nThinking\t%t\nVision\t%t\n", args[0], info.RuntimeName, info.DeviceKind, info.DeviceID, info.ModelMaxContext, info.EffectiveContext, info.ChatTemplateSupportsToolCalls, info.ChatTemplateSupportsThinking, info.SupportsVision)
			if info.Reason != "" {
				fmt.Fprintf(w, "Capacity reason\t%s\n", info.Reason)
			}
			return w.Flush()
		}}
	cmd.Flags().Bool("json", false, "Print the worker's full model report as JSON.")
	return cmd
}

func newNativePSCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ps", Short: "Show the local worker's resident model without starting it.", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := modeldconn.Status(cmd.Context())
			if errors.Is(err, modeldprobe.ErrNotRunning) || errors.Is(err, modeldprobe.ErrNotInstalled) {
				status = transport.DaemonStatus{}
			} else if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			if status.Backend == "shared" && len(status.Workers) > 0 {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "MODEL\tENGINE\tSTATE\tWORKER")
				for _, worker := range status.Workers {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", worker.Spec.Model, worker.Spec.Backend, worker.State, worker.Instance)
				}
				return w.Flush()
			}
			if status.Active == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No resident native model.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "MODEL\tENGINE\tSTATE\tGENERATION")
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", status.Active.ModelName, status.Backend, status.State, status.Active.Generation)
			return w.Flush()
		}}
	cmd.Flags().Bool("json", false, "Print the worker's resident slot status as JSON.")
	return cmd
}

func newNativeStopCmd() *cobra.Command {
	return &cobra.Command{Use: "stop <model>", Short: "Unload the named resident native model; keep the worker running.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := modeldconn.StopModel(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Unloaded %s\n", args[0])
			return nil
		}}
}

func preselectNativeModel(models []string, defaultModel string) string {
	for _, m := range models {
		if m == defaultModel {
			return m
		}
	}
	return models[0]
}

func promptNativeModelMenu(out io.Writer, scanner *bufio.Scanner, models []string, preselected string) string {
	fmt.Fprintln(out, "  Installed native models:")
	fmt.Fprintln(out, "")
	preselectedIdx := 1
	for i, m := range models {
		marker := ""
		if m == preselected {
			marker = "  (default)"
			preselectedIdx = i + 1
		}
		fmt.Fprintf(out, "    %d. %s%s\n", i+1, m, marker)
	}
	fmt.Fprintln(out, "")
	for {
		answer := promptLine(out, scanner, fmt.Sprintf("  Model [%d]", preselectedIdx), "")
		model, ok := resolveNativeModelChoice(answer, models, preselected)
		if ok {
			return model
		}
		fmt.Fprintf(out, "  Please enter a number between 1 and %d, or a model id.\n", len(models))
	}
}

func resolveNativeModelChoice(answer string, models []string, preselected string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return preselected, true
	}
	if n, err := strconv.Atoi(answer); err == nil {
		if n >= 1 && n <= len(models) {
			return models[n-1], true
		}
		return "", false
	}
	return answer, true
}
