package contenoxcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	plannerChainFilename  = "agent-planner.json"
	reviewerChainFilename = "agent-reviewer.json"

	DefaultMissionAgent  = "agent-reviewer"
	DefaultMissionPolicy = "read_only"

	missionAgentConfigKey  = "default-mission-agent"
	missionPolicyConfigKey = "default-mission-policy"
)

func seedMissionAgentChains(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	var created []string
	for _, f := range []struct{ name, content string }{
		{plannerChainFilename, initPlannerChain},
		{reviewerChainFilename, initReviewerChain},
	} {
		dst := filepath.Join(dir, f.name)
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return created, fmt.Errorf("stat %s: %w", dst, err)
		}
		if err := os.WriteFile(dst, []byte(f.content), 0o644); err != nil {
			return created, fmt.Errorf("write %s: %w", dst, err)
		}
		created = append(created, dst)
	}
	return created, nil
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
