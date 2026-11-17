package contenoxcli

import (
	"encoding/json"
	"fmt"

	"github.com/contenox/runtime/runtime/modeldmanagement"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/spf13/cobra"
)

var modeldCmd = &cobra.Command{Use: "modeld", Short: modeldmanagement.Contract.Summary, Long: modeldmanagement.Contract.Description}
var modeldInstallCmd = &cobra.Command{Use: "install", Short: "Install a compatible verified local worker.", Args: cobra.NoArgs, RunE: runModeldInstall}
var modeldStatusCmd = &cobra.Command{Use: "status", Short: "Probe the local worker.", Args: cobra.NoArgs, RunE: runModeldStatus}

func modeldService(cmd *cobra.Command) (modeldmanagement.Service, error) {
	dataRoot, err := cmd.Flags().GetString("data-root")
	if err != nil {
		return nil, err
	}
	if dataRoot == "" {
		dataRoot = modeldconn.DataRootFromContext(cmd.Context())
	}
	baseURL, err := cmd.Flags().GetString("base-url")
	if err != nil {
		return nil, err
	}
	return modeldmanagement.New(dataRoot, baseURL, cliVersion())
}

func runModeldInstall(cmd *cobra.Command, _ []string) error {
	service, err := modeldService(cmd)
	if err != nil {
		return err
	}
	backend, err := cmd.Flags().GetString("backend")
	if err != nil {
		return err
	}
	result, err := modeldmanagement.Execute(cmd.Context(), service, modeldmanagement.Request{Action: modeldmanagement.ActionInstall, Backend: backend, Progress: cmd.ErrOrStderr()})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "modeld %s: %s\n", result.Install.Version, result.Install.LauncherPath)
	return err
}

func runModeldStatus(cmd *cobra.Command, _ []string) error {
	service, err := modeldService(cmd)
	if err != nil {
		return err
	}
	result, err := modeldmanagement.Execute(cmd.Context(), service, modeldmanagement.Request{Action: modeldmanagement.ActionStatus})
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			State    string `json:"state"`
			Binary   string `json:"binary,omitempty"`
			Endpoint string `json:"endpoint,omitempty"`
			Backend  string `json:"backend,omitempty"`
		}{State: result.Status.State.String(), Binary: result.Status.Binary, Endpoint: result.Status.Endpoint, Backend: result.Status.Backend})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\nBinary: %s\nEndpoint: %s\nBackend: %s\n", result.Status.State, result.Status.Binary, result.Status.Endpoint, result.Status.Backend)
	return err
}

func init() {
	modeldCmd.PersistentFlags().String("data-root", "", "Worker data directory (defaults to CONTENOX_DATA_ROOT or ~/.contenox).")
	modeldCmd.PersistentFlags().String("base-url", "", "Worker release index base URL.")
	modeldInstallCmd.Flags().String("backend", "llama", "Required backend: llama or openvino.")
	modeldStatusCmd.Flags().Bool("json", false, "Print status as JSON.")
	modeldCmd.AddCommand(modeldInstallCmd, modeldStatusCmd)
	reservedSubcommands["modeld"] = true
	rootCmd.AddCommand(modeldCmd)
}
