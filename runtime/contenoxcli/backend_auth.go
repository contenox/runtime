package contenoxcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/spf13/cobra"
)

func runCodexSetup(cmd *cobra.Command, out io.Writer, scanner *bufio.Scanner) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	authCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	db, _, err := openBackendDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := protectCredentialDB(cmd, db); err != nil {
		return err
	}
	if err := registerSetupBackend(ctx, db, modelauth.ProviderType, "", modelauth.BaseURL); err != nil {
		return err
	}
	store := runtimetypes.New(db.WithoutTransaction())
	backends, err := store.ListBackends(ctx, nil, 100)
	if err != nil {
		return err
	}
	var backend *runtimetypes.Backend
	for _, b := range backends {
		if b.Type == modelauth.ProviderType {
			backend = b
			break
		}
	}
	if backend == nil {
		return fmt.Errorf("subscription backend was not registered")
	}

	auth := modelauth.New(db)
	status, err := auth.Status(authCtx, backend.ID)
	if err != nil {
		return err
	}
	if status.State == "login_required" {
		fmt.Fprintln(out, "  Enable device-code login in ChatGPT Settings → Security (or ask your workspace administrator).")
		if err := auth.Login(authCtx, backend.ID, showDeviceCode(out)); err != nil {
			return err
		}
	}
	invalidateBackendModelCache(ctx, cmd.ErrOrStderr(), db, backend.ID)

	catalog, err := modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{Type: modelauth.ProviderType, BaseURL: modelauth.BaseURL},
		modelrepo.WithCatalogAuthorizer(func(ctx context.Context) (http.Header, error) { return auth.Headers(ctx, backend.ID) }),
	)
	if err != nil {
		return err
	}
	models, err := catalog.ListModels(authCtx)
	if err != nil {
		return fmt.Errorf("list ChatGPT models: %w", err)
	}
	if err := authCtx.Err(); err != nil {
		return err
	}
	stop()

	name, err := promptCatalogModel(out, scanner, models)
	if err != nil {
		return err
	}

	exec, commit, release, err := db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	config := runtimetypes.New(exec)
	if err := clikv.WriteConfig(ctx, config, "global", "default-provider", modelauth.ProviderType); err != nil {
		return err
	}
	if err := clikv.WriteConfig(ctx, config, "global", "default-model", name); err != nil {
		return err
	}
	if err := commit(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "  ✓ Selected %s via ChatGPT subscription.\n", name)
	return nil
}

func promptCatalogModel(out io.Writer, scanner *bufio.Scanner, models []modelrepo.ObservedModel) (string, error) {
	if len(models) == 0 {
		return "", fmt.Errorf("no models available; inference defaults unchanged")
	}
	fmt.Fprintln(out, "\n  Available models:")
	fmt.Fprintln(out)
	for i, model := range models {
		fmt.Fprintf(out, "    %d. %s\n", i+1, model.Name)
	}
	fmt.Fprintln(out)
	choice := promptChoiceOrQuit(out, scanner, "  Model", len(models), false)
	if choice == promptEOF {
		return "", fmt.Errorf("model selection cancelled; login retained and inference defaults unchanged")
	}
	if choice < 0 {
		return "", fmt.Errorf("model selection cancelled; login retained and inference defaults unchanged")
	}
	return models[choice].Name, nil
}

func init() {
	backendCmd.AddCommand(&cobra.Command{
		Use:   "login <name>",
		Short: "Sign in to a ChatGPT subscription backend using a device code.",
		Long: `Sign in to a ChatGPT subscription backend (type openai-codex) with a device code.

Enable device-code login in ChatGPT Settings → Security first; managed
workspaces may need an administrator to allow it. The token is stored locally
and refreshed by the runtime. Signing in does not change the default model.`,
		Args: cobra.ExactArgs(1),
		RunE: backendLogin,
	})
	backendCmd.AddCommand(&cobra.Command{
		Use:   "logout <name>",
		Short: "Clear a subscription backend's locally stored login.",
		Args:  cobra.ExactArgs(1),
		RunE:  backendLogout,
	})
}

func showDeviceCode(out io.Writer) func(modelauth.DeviceCode) error {
	return func(code modelauth.DeviceCode) error {
		_, err := fmt.Fprintf(out, "  Open %s\n  Enter code: %s\n  Expires: %s\n  Waiting for authorization…\n",
			code.URL, code.Code, code.ExpiresAt.Format(time.RFC3339))
		return err
	}
}

func backendLogin(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	db, _, err := openBackendDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	b, err := runtimetypes.New(db.WithoutTransaction()).GetBackendByName(ctx, args[0])
	if errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("no connection named %q; `contenox-runtime backend list` shows the saved connections", args[0])
	}
	if err != nil {
		return err
	}
	if b.Type != modelauth.ProviderType {
		return fmt.Errorf("device login is only supported for openai-codex backends; %q is %s", args[0], b.Type)
	}
	if err := protectCredentialDB(cmd, db); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "  Enable device-code login in ChatGPT Settings → Security first. Managed workspaces may need administrator permission.")
	if err := modelauth.New(db).Login(ctx, b.ID, showDeviceCode(out)); err != nil {
		return err
	}
	invalidateBackendModelCache(ctx, cmd.ErrOrStderr(), db, b.ID)
	fmt.Fprintln(out, "  Signed in. Login does not verify model access or change defaults. Run: contenox-runtime model list")
	return nil
}

func backendLogout(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	db, _, err := openBackendDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	b, err := runtimetypes.New(db.WithoutTransaction()).GetBackendByName(ctx, args[0])
	if errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("no connection named %q; `contenox-runtime backend list` shows the saved connections", args[0])
	}
	if err != nil {
		return err
	}
	if err := modelauth.New(db).Logout(ctx, b.ID); err != nil {
		return err
	}
	invalidateBackendModelCache(ctx, cmd.ErrOrStderr(), db, b.ID)
	fmt.Fprintln(cmd.OutOrStdout(), "  Local login cleared. Backend configuration retained; remote authorization was not revoked.")
	return nil
}

func protectCredentialDB(cmd *cobra.Command, db libdb.DBManager) error {
	if db.WithoutTransaction().DriverName() != "sqlite" {
		return nil
	}
	path, err := resolveDBPath(cmd)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Chmod(path+suffix, 0o600)
		if suffix != "" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("protect credential database permissions: %w", err)
		}
	}
	return nil
}
