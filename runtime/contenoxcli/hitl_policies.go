package contenoxcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/contenox/runtime/runtime/agentdecl"
)

var retiredHITLPolicyHashes = map[string][]string{
	"hitl-policy-default.json": {
		"483e9ff61a0cc2e1f7bafc5125bb9890db961b9351de77f6aef34d3df785259a",
		"570b2756482bcc4ad662bdc446424f53d79b620ab2abd22f4192390858274271",
		"931f0d41dea98ac0eb409cb678995c1141d62ccc6ddb63f4c5adb86a2d4fd807",
	},
	"hitl-policy-strict.json": {
		"0b96f1ff338b04dd7c5ceb66306ca54bc64d0fa20f7332b48fe42dd138504dfc",
		"47bfe6e48dc5e7c9d378384b40519fa3d22c83561ec5d6e04264da04727c74f7",
		"7d862938e1176dffce0ac0664a8db687991db7caa730db232a740dbd298ba46f",
	},
	"hitl-policy-dev.json": {
		"57d491b25f0e95caf9e1df9af05d975874ec5c75b87b7f1d0bffb9d0bfbfa33c",
		"881bed7301c7d52ca1dd8d5845c113876aab0d173dde4ea8004c9fdfbd8da6cc",
		"9bee259b60b2c7363cee2fb38e9a0303f8f4014379c5263e39f73ad5d52d9ea9",
		"f5c604d9bba7b0efb5cbd9eedad28fbf82666ab9bdd9faae813280edfccd730f",
	},
	"hitl-policy-acp.json": {
		"48dafb0bc4fbf81ebc05a0c9ca2762d64ad3bc72be5f1952719bb668c0095b2c",
		"692c7cd0c07fd69159861aed97699bc88c7e5ef3e1373cc5f8e87a0c962f328b",
		"879627fe88a7705425fff0edc14209f51896435c42ee73bd0f6c90c8874b91d1",
	},
	"hitl-policy-acpx.json": {
		"3244c466a3f1bf56795dbd8b416cbc28da9140bb7c17fb1673797e600d04766a",
		"a28e4a24edeea6f8cbeec31159c4ce4924d0c929139588e5a3867c7072394ef4",
		"c339b0542e2038b8766aa45bbac9add5190b752ec15fb4404cd15fb69d5bf24f",
	},
	"hitl-policy-oracle.json": {
		"398fb292aa73f5577b5c0c73ae5bb44284d21483666017126173d50693b30d56",
	},
}

const retiredHITLPolicyStateFilename = ".hitl-policies-state.json"

func retireShippedHITLPolicies(dir string) (removed, kept []string, err error) {
	for name, hashes := range retiredHITLPolicyHashes {
		path := filepath.Join(dir, name)
		data, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return removed, kept, fmt.Errorf("read %s: %w", path, readErr)
		}
		sum := sha256.Sum256(data)
		if !slices.Contains(hashes, hex.EncodeToString(sum[:])) {
			kept = append(kept, path)
			continue
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return removed, kept, fmt.Errorf("remove %s: %w", path, rmErr)
		}
		removed = append(removed, path)
	}
	if len(kept) == 0 {
		_ = os.Remove(filepath.Join(dir, retiredHITLPolicyStateFilename))
	}
	sort.Strings(removed)
	sort.Strings(kept)
	return removed, kept, nil
}

func reportRetiredHITLPolicies(dir string) {
	removed, kept, err := retireShippedHITLPolicies(dir)
	if err != nil {
		slog.Warn("agent declarations: could not retire the old JSON HITL policies", "dir", dir, "error", err)
		return
	}
	if len(removed) > 0 {
		slog.Info("agent declarations: retired the old JSON HITL policies; they compile from [envelopes] in agents.toml", "files", removed)
	}
	for _, path := range kept {
		slog.Warn("agent declarations: this edited HITL policy is shadowed by the envelope of the same name and has no effect; move your changes into [envelopes] in agents.toml",
			"file", path, "envelope", envelopeNameOf(path))
	}
}

func envelopeNameOf(path string) string {
	name, _ := agentdecl.EnvelopeName(filepath.Base(path))
	return name
}

func knownPolicyNames(contenoxDir string) []string {
	cfg, err := agentdecl.Shipped()
	if err != nil {
		return nil
	}
	if root, rootErr := agentdecl.LocalRoot(contenoxDir); rootErr == nil {
		if layered, loadErr := agentdecl.Load(context.Background(), root); loadErr == nil {
			cfg = layered
		}
	}
	names := make([]string, 0, len(cfg.Envelopes))
	for name := range cfg.Envelopes {
		if name != "default" {
			names = append(names, agentdecl.EnvelopePolicyFile(name))
		}
	}
	sort.Strings(names)
	if _, ok := cfg.Envelopes["default"]; ok {
		names = append([]string{agentdecl.EnvelopePolicyFile("default")}, names...)
	}
	return names
}
