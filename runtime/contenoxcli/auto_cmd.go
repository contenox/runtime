package contenoxcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/spf13/cobra"
)

var autoCmd = &cobra.Command{
	Use:   "auto [us|eu|china|gus]",
	Short: "Choose, install, and verify a local model for this machine.",
	Long:  "Detect available hardware, select a compatible curated model, install the native worker and model, and verify tool calling before saving defaults.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runAuto,
}

func init() {
	autoCmd.Flags().String("origin", "", "Model developer origin: us, eu, china or gus.")
	autoCmd.Flags().Bool("dry-run", false, "Show the hardware and selection without installing or changing defaults.")
	autoCmd.Flags().Bool("refresh", false, "Select from the current catalog instead of reusing the configured model.")
	autoCmd.Flags().String("worker-base-url", "", "Native worker release index base URL.")
	reservedSubcommands["auto"] = true
	rootCmd.AddCommand(autoCmd)
}

func runAuto(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	origin, _ := cmd.Flags().GetString("origin")
	if len(args) != 0 {
		if origin != "" && origin != args[0] {
			return fmt.Errorf("choose either positional origin or --origin")
		}
		origin = args[0]
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	refresh, _ := cmd.Flags().GetBool("refresh")
	baseURL, _ := cmd.Flags().GetString("worker-base-url")
	root := modeldconn.DataRootFromContext(ctx)
	opts := autosetup.Options{
		Origin: origin, Backend: strings.TrimSpace(os.Getenv("CONTENOX_MODELD_BACKEND")),
		KVCacheType: workerKVCacheType(), DataRoot: root, WorkerBaseURL: baseURL, Refresh: refresh,
		Progress: cmd.OutOrStdout(),
		Report: func(value autosetup.Progress) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), value.Message)
			return err
		},
	}
	dbPath, err := resolveDBPath(cmd)
	if err != nil {
		return err
	}
	var db libdb.DBManager
	if _, statErr := os.Stat(dbPath); statErr == nil {
		db, err = OpenDBAt(ctx, dbPath)
		if err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if db != nil {
		defer db.Close()
	}
	var setup *autosetup.Service
	if db == nil {
		setup, err = autosetup.NewPlanner(libtracker.NoopTracker{})
	} else {
		setup, err = autosetup.New(db, libtracker.NoopTracker{})
	}
	if err != nil {
		return err
	}
	plan, err := setup.Plan(ctx, opts)
	if err != nil {
		return err
	}
	if dry {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Hardware            modelhardware.Hardware        `json:"hardware"`
			Model               modelregistry.ModelDescriptor `json:"model"`
			Reused              bool                          `json:"reused"`
			EstimatedHotContext int                           `json:"estimated_hot_context"`
		}{plan.Hardware, plan.Model, plan.Reused, plan.EstimatedHotContext})
	}
	if db == nil {
		db, err = OpenDBAt(ctx, dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		setup, err = autosetup.New(db, libtracker.NoopTracker{})
		if err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, plan.Hardware.Summary)
	fmt.Fprintf(out, "Selected %s (%s, %s, %s); download %s.\n", plan.Model.Label(), plan.Model.BackendType(), plan.Model.Origin, plan.Model.License, modelstore.FormatSize(plan.Model.SizeBytes+plan.Model.MMProjSizeBytes))
	fmt.Fprintf(out, "Estimated hot context: %d tokens at %s KV; %d is the floor for agentic work.\n",
		plan.EstimatedHotContext, kvLabelForDisplay(opts.KVCacheType), modelregistry.MinAgenticHotContext)
	if opts.KVCacheType == "" {
		fmt.Fprintln(out, "  Set CONTENOX_LLAMA_KV_CACHE_TYPE=q8_0 before starting the worker to halve what the window costs in device memory.")
	}
	fmt.Fprintf(out, "Model store: %s\n", modelstore.Dir(root, ""))
	if plan.Reused {
		fmt.Fprintln(out, "Reusing configured model; --refresh selects from the current catalog.")
	}
	fmt.Fprintln(out, "Verifying native inference and tool calling...")
	result, err := setup.Apply(ctx, plan, opts)
	if err != nil {
		return err
	}
	if err := setup.SaveDefaults(ctx, plan.Model); err != nil {
		return err
	}
	if err := RunGlobalInit(out); err != nil {
		return err
	}
	fmt.Fprintf(out, "Ready: %s; worker reports %d hot context tokens. Run contenox-runtime to start working.\n", plan.Model.Name, result.HotContext)
	return nil
}

func workerKVCacheType() string {
	return strings.TrimSpace(os.Getenv("CONTENOX_LLAMA_KV_CACHE_TYPE"))
}

func kvLabelForDisplay(kvType string) string {
	if kvType == "" {
		return "f16"
	}
	return kvType
}

func previousAutoModel(ctx context.Context, cmd *cobra.Command, origin, backend string, budget int64) (modelregistry.ModelDescriptor, bool, error) {
	path, err := resolveDBPath(cmd)
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return modelregistry.ModelDescriptor{}, false, nil
		}
		return modelregistry.ModelDescriptor{}, false, err
	}
	db, err := OpenDBAt(ctx, path)
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	if db == nil {
		return modelregistry.ModelDescriptor{}, false, nil
	}
	defer db.Close()
	setup, err := autosetup.New(db, libtracker.NoopTracker{})
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	return setup.Previous(ctx, origin, backend, budget, workerKVCacheType())
}

func verifyAutoModel(ctx context.Context, out io.Writer, model modelregistry.ModelDescriptor, planned int) (int, error) {
	return autosetup.Verify(ctx, model, planned, workerKVCacheType(), out)
}

func saveAutoDefaults(ctx context.Context, cmd *cobra.Command, model modelregistry.ModelDescriptor) error {
	db, _, err := openBackendDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()
	setup, err := autosetup.New(db, libtracker.NoopTracker{})
	if err != nil {
		return err
	}
	return setup.SaveDefaults(ctx, model)
}
