package contenoxcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	DefaultMissionAgent  = "reviewer"
	DefaultMissionPolicy = "read_only"

	missionAgentConfigKey  = "default-mission-agent"
	missionPolicyConfigKey = "default-mission-policy"
)

var retiredAgentChains = map[string]struct {
	successor string
	hashes    []string
}{
	"agent-planner.json":  {"planner", []string{"b5517890d1290d80b7daa11099d0065f926a4b26753530f73b07cb00ce455c59"}},
	"agent-reviewer.json": {"reviewer", []string{"16a188fc4f63a373f13aa82f827b3710a94d56a82b133214d2608359dcdb08d3"}},
}

func retireSeededAgentChains(dir string) ([]string, error) {
	var removed []string
	for name, retired := range retiredAgentChains {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(data)
		if !slices.Contains(retired.hashes, hex.EncodeToString(sum[:])) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("remove %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	sort.Strings(removed)
	return removed, nil
}

func migrateRetiredMissionAgent(ctx context.Context, store runtimetypes.Store) (string, error) {
	var current string
	if err := store.GetKV(ctx, clikv.Prefix+missionAgentConfigKey, &current); err != nil {
		return "", nil
	}
	retired, ok := retiredAgentChains[current+".json"]
	if !ok {
		return "", nil
	}
	if err := clikv.SetString(ctx, store, missionAgentConfigKey, retired.successor); err != nil {
		return "", fmt.Errorf("set %s: %w", missionAgentConfigKey, err)
	}
	return missionAgentConfigKey + "=" + retired.successor, nil
}

type missionDefaultCheck struct {
	agent  func(ctx context.Context, name string) bool
	policy func(ctx context.Context, name string) bool
}

func seedMissionDefaults(ctx context.Context, store runtimetypes.Store, check missionDefaultCheck) ([]string, error) {
	var seeded []string
	for _, d := range []struct {
		key, value string
		ok         func(ctx context.Context, name string) bool
	}{
		{missionAgentConfigKey, DefaultMissionAgent, check.agent},
		{missionPolicyConfigKey, DefaultMissionPolicy, check.policy},
	} {
		var current string
		if err := store.GetKV(ctx, clikv.Prefix+d.key, &current); !errors.Is(err, libdb.ErrNotFound) {
			continue
		}
		if d.ok != nil && !d.ok(ctx, d.value) {
			continue
		}
		if err := clikv.SetString(ctx, store, d.key, d.value); err != nil {
			return seeded, fmt.Errorf("set %s: %w", d.key, err)
		}
		seeded = append(seeded, d.key+"="+d.value)
	}
	return seeded, nil
}

func seedGlobalMissionDefaults(out io.Writer) {
	dbPath, err := globalDBPath()
	if err != nil {
		return
	}
	ctx := libtracker.WithNewRequestID(context.Background())
	db, err := OpenDBAt(ctx, dbPath)
	if err != nil {
		return
	}
	defer db.Close()
	seeded, err := seedMissionDefaults(ctx, runtimetypes.New(db.WithoutTransaction()), missionDefaultCheck{})
	if err != nil {
		fmt.Fprintf(out, "  Could not set mission defaults: %v\n", err)
		return
	}
	for _, s := range seeded {
		fmt.Fprintf(out, "  Set %s\n", s)
	}
}
