package contextasm_test

import (
	"errors"
	"testing"

	"github.com/contenox/runtime/internal/modeld/contextasm"
)

func TestUnit_ContextasmAssembleManifest_StableHashIgnoresVolatileChange(t *testing.T) {
	id := contextasm.ManifestIdentity{
		ProfileID:            "coder",
		Backend:              "test",
		ModelDigest:          "model",
		PromptFormat:         "template",
		PromptTemplateDigest: "template-digest",
		RuntimeDigest:        "runtime",
	}
	a := []contextasm.Segment{
		{Kind: contextasm.KindSystem, Content: "rules"},
		{Kind: contextasm.KindUserTurn, Content: "turn a"},
	}
	b := []contextasm.Segment{
		{Kind: contextasm.KindUserTurn, Content: "turn b"},
		{Kind: contextasm.KindSystem, Content: "rules"},
	}

	pa, ma := contextasm.AssembleManifest(a, id)
	pb, mb := contextasm.AssembleManifest(b, id)

	if pa == pb {
		t.Fatal("full logical prompt should include volatile change")
	}
	if ma.StableByteHash != mb.StableByteHash {
		t.Fatalf("stable hash changed for volatile-only edit: %s != %s", ma.StableByteHash, mb.StableByteHash)
	}
	if ma.ProfileID != id.ProfileID || ma.Backend != id.Backend || ma.RuntimeDigest != id.RuntimeDigest {
		t.Fatalf("identity was not copied into manifest: %+v", ma)
	}
	if len(ma.Segments) != 2 || !ma.Segments[0].Stable || ma.Segments[1].Stable {
		t.Fatalf("segments not classified stable/volatile: %+v", ma.Segments)
	}
}

func TestUnit_ContextasmManifest_CompatibilityIgnoresStableByteHash(t *testing.T) {
	base := contextasm.ContextManifest{
		Backend:              "test",
		ModelDigest:          "model",
		PromptFormat:         "format",
		PromptTemplateDigest: "template",
		RuntimeDigest:        "runtime",
		StableByteHash:       "stable-a",
	}
	next := base
	next.StableByteHash = "stable-b"
	if ok, reason := base.CompatibleRuntime(next); !ok {
		t.Fatalf("stable hash change should not block runtime compatibility: %s", reason)
	}

	next = base
	next.ModelDigest = "other"
	if ok, reason := base.CompatibleRuntime(next); ok || reason == "" {
		t.Fatalf("model digest change should block compatibility, ok=%t reason=%q", ok, reason)
	}
}

func TestUnit_ContextasmCacheClass_MapsKindsAndOrders(t *testing.T) {
	cases := map[contextasm.SegmentKind]contextasm.CacheClass{
		contextasm.KindSystem:    contextasm.ClassTaskPinned,
		contextasm.KindTools:     contextasm.ClassTaskPinned,
		contextasm.KindRepoRules: contextasm.ClassTaskPinned,
		contextasm.KindRepoMap:   contextasm.ClassRepoMap,
		contextasm.KindPinned:    contextasm.ClassRepoMap,
		contextasm.KindDiff:      contextasm.ClassVolatile,
		contextasm.KindTerminal:  contextasm.ClassVolatile,
		contextasm.KindUserTurn:  contextasm.ClassVolatile,
	}
	for k, want := range cases {
		if got := k.CacheClass(); got != want {
			t.Errorf("%s.CacheClass() = %s, want %s", k.Tag(), got.Tag(), want.Tag())
		}
	}
	// Volatile is evicted before repo-map, which is evicted before task-pinned.
	if !contextasm.ClassVolatile.MoreEvictableThan(contextasm.ClassRepoMap) || !contextasm.ClassRepoMap.MoreEvictableThan(contextasm.ClassTaskPinned) {
		t.Fatal("eviction ordering must be volatile > repo_map > task_pinned")
	}
	if contextasm.ClassTaskPinned.MoreEvictableThan(contextasm.ClassVolatile) {
		t.Fatal("task_pinned must not be more evictable than volatile")
	}
}

func TestUnit_ContextasmAssembleManifest_PopulatesCacheClass(t *testing.T) {
	_, m := contextasm.AssembleManifest([]contextasm.Segment{
		{Kind: contextasm.KindSystem, Content: "sys"},
		{Kind: contextasm.KindUserTurn, Content: "hi"},
	}, contextasm.ManifestIdentity{})
	byKind := map[string]string{}
	for _, s := range m.Segments {
		byKind[s.Kind] = s.CacheClass
	}
	if byKind["system"] != "task_pinned" {
		t.Errorf("system cache_class = %q, want task_pinned", byKind["system"])
	}
	if byKind["user"] != "volatile" {
		t.Errorf("user cache_class = %q, want volatile", byKind["user"])
	}
}

func TestUnit_ContextasmBuildSplitManifest_NormalizesIdentityHashesAndClasses(t *testing.T) {
	stable := "rules"
	volatile := "question"
	manifest, err := contextasm.BuildSplitManifest(stable, volatile, []contextasm.ManifestSegment{
		{Kind: "system", Stable: true, ByteStart: 0, ByteEnd: len(stable)},
		{Kind: "user", Stable: false, ByteStart: len(stable), ByteEnd: len(stable) + len(volatile)},
	}, contextasm.ManifestIdentity{
		ProfileID:            "coder",
		Backend:              "llama",
		BackendVersion:       "v1",
		ModelDigest:          "model",
		PromptFormat:         "chatml",
		PromptTemplateDigest: "template",
		RuntimeDigest:        "runtime",
		AddBOS:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ProfileID != "coder" || manifest.Backend != "llama" || manifest.RuntimeDigest != "runtime" || !manifest.AddBOS {
		t.Fatalf("identity not copied into manifest: %+v", manifest)
	}
	if manifest.StableBytes != len(stable) || manifest.TotalBytes != len(stable)+len(volatile) {
		t.Fatalf("byte counts = stable %d total %d", manifest.StableBytes, manifest.TotalBytes)
	}
	if manifest.StableByteHash != contextasm.HashString(stable) {
		t.Fatalf("StableByteHash = %q, want hash of stable text", manifest.StableByteHash)
	}
	if len(manifest.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(manifest.Segments))
	}
	if manifest.Segments[0].ByteHash != contextasm.HashString(stable) || manifest.Segments[0].CacheClass != contextasm.ClassTaskPinned.Tag() {
		t.Fatalf("stable segment not normalized: %+v", manifest.Segments[0])
	}
	if manifest.Segments[1].ByteHash != contextasm.HashString(volatile) || manifest.Segments[1].CacheClass != contextasm.ClassVolatile.Tag() {
		t.Fatalf("volatile segment not normalized: %+v", manifest.Segments[1])
	}
}

func TestUnit_ContextasmBuildSplitManifest_RejectsInvalidRangesAndHashes(t *testing.T) {
	_, err := contextasm.BuildSplitManifest("rules", "ask", []contextasm.ManifestSegment{
		{Kind: "user", Stable: false, ByteStart: 0, ByteEnd: 3},
	}, contextasm.ManifestIdentity{})
	if !errors.Is(err, contextasm.ErrManifestMismatch) {
		t.Fatalf("volatile segment before stable split err = %v, want ErrManifestMismatch", err)
	}

	_, err = contextasm.BuildSplitManifest("rules", "", []contextasm.ManifestSegment{
		{Kind: "system", Stable: true, ByteStart: 0, ByteEnd: 5, ByteHash: "wrong"},
	}, contextasm.ManifestIdentity{})
	if !errors.Is(err, contextasm.ErrManifestMismatch) {
		t.Fatalf("bad byte hash err = %v, want ErrManifestMismatch", err)
	}
}

func TestUnit_ManifestMismatchError_TrimsRepeatedPrefix(t *testing.T) {
	err := contextasm.NewManifestMismatchError("contextasm: context manifest mismatch: contextasm: context manifest mismatch: stable prefix changed")
	if !errors.Is(err, contextasm.ErrManifestMismatch) {
		t.Fatalf("error = %v, want ErrManifestMismatch", err)
	}
	if got, want := err.Error(), "contextasm: context manifest mismatch: stable prefix changed"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
