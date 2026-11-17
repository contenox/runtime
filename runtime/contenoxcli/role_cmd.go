package contenoxcli

import (
	"fmt"
	"io"
	"strings"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/spf13/cobra"
)

var roleCmd = &cobra.Command{
	Use:   "role",
	Short: "Turn an optional inference role on or off.",
	Long:  "Turn an optional inference role on or off. Optional roles: " + strings.Join(inferencesetup.OptionalRoles(), ", ") + ".",
}

var roleOnCmd = &cobra.Command{
	Use:   "on <role>",
	Short: "Enable a configured and verified optional inference role.",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoleOn,
}

var roleOffCmd = &cobra.Command{
	Use:   "off <role>",
	Short: "Disable an optional inference role and keep its model assignment.",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoleOff,
}

func init() {
	reservedSubcommands["role"] = true
	roleCmd.AddCommand(roleOnCmd, roleOffCmd)
	rootCmd.AddCommand(roleCmd)
}

func runRoleOn(cmd *cobra.Command, args []string) error {
	return runRoleEnabled(cmd, args[0], true)
}

func runRoleOff(cmd *cobra.Command, args []string) error {
	return runRoleEnabled(cmd, args[0], false)
}

func runRoleEnabled(cmd *cobra.Command, role string, enabled bool) error {
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
	if enabled {
		if err := state.RunBackendCycle(ctx); err != nil {
			return fmt.Errorf("refresh the inference catalog before turning %s on: %w", role, err)
		}
	}
	setup, _, closeSetup, err := inferenceSetupFactory(db, bus, nil, state, tracker, "")(ctx)
	if err != nil {
		return err
	}
	defer closeSetup()
	if err := setup.SetRoleEnabled(ctx, role, enabled); err != nil {
		return err
	}
	roles, err := setup.Roles(ctx)
	if err != nil {
		return err
	}
	for _, current := range roles {
		if current.Role == role {
			printRoleState(cmd.OutOrStdout(), current)
			return nil
		}
	}
	return fmt.Errorf("role %q is missing from the configuration", role)
}

func printRoleState(w io.Writer, role inferencesetup.RoleStatus) {
	if !role.Enabled {
		fmt.Fprintf(w, "Role %s: off\n", role.Role)
		return
	}
	fmt.Fprintf(w, "Role %s: on, %s/%s\n", role.Role, role.Provider, role.Model)
}
