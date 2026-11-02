package contenoxcli

import (
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/spf13/cobra"
)

func chainUnitArgs(dbPath, dataDir string, pinned acpsvc.LiveDefaults) []string {
	var args []string
	add := func(flag, value string) {
		if v := strings.TrimSpace(value); v != "" {
			args = append(args, "--"+flag, v)
		}
	}
	add("db", dbPath)
	add("data-dir", dataDir)
	add("provider", pinned.Provider)
	add("model", pinned.Model)
	add("alt-provider", pinned.AltProvider)
	add("alt-model", pinned.AltModel)
	return args
}

func pinnedModelFlags(cmd *cobra.Command) acpsvc.LiveDefaults {
	var p acpsvc.LiveDefaults
	if cmd == nil {
		return p
	}
	get := func(name string) string {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			f = cmd.Root().PersistentFlags().Lookup(name)
		}
		if f == nil || !f.Changed {
			return ""
		}
		return strings.TrimSpace(f.Value.String())
	}
	p.Model = get("model")
	p.Provider = get("provider")
	p.AltModel = get("alt-model")
	p.AltProvider = get("alt-provider")
	return p
}

func acpContenoxDir(cmd *cobra.Command) (string, error) {
	if cmd != nil {
		if dataDir, _ := cmd.Root().PersistentFlags().GetString("data-dir"); strings.TrimSpace(dataDir) != "" {
			return filepath.Abs(dataDir)
		}
	}
	return globalContenoxDir()
}
