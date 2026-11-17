package llama

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
)

type embeddingMemorySource struct {
	snapshots []capacity.DeviceSnapshot
	err       error
	calls     int
}

func (s *embeddingMemorySource) FreeBytes() (int64, error) {
	snapshot, err := s.Snapshot()
	return snapshot.FreeBytes, err
}

func (s *embeddingMemorySource) Snapshot() (capacity.DeviceSnapshot, error) {
	s.calls++
	if s.err != nil {
		return capacity.DeviceSnapshot{}, s.err
	}
	return s.snapshots[min(s.calls-1, len(s.snapshots)-1)], nil
}

func TestUnit_ServiceEmbeddingKeepsPlacementUntilRelease(t *testing.T) {
	path := writeTestModelProfile(t, ggufParams{BlockCount: 4, ContextLength: 4096})
	policy := embeddingLifecyclePolicy(t)
	memory := &embeddingMemorySource{snapshots: []capacity.DeviceSnapshot{
		{Kind: "gpu", TotalBytes: 1000, FreeBytes: 100},
		{Kind: "gpu", TotalBytes: 1000, FreeBytes: 10},
	}}
	var configs []transport.Config
	releases := 0
	setEmbeddingLifecycleHooks(t, func(_ context.Context, gotPath string, cfg Config, _ string) ([]float64, error) {
		if gotPath != path {
			t.Fatalf("embedding path = %q, want %q", gotPath, path)
		}
		configs = append(configs, cfg)
		return []float64{0.25, 0.75}, nil
	}, func() error { releases++; return nil })
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(memory), WithHostMemorySource(staticMemory(1000)))
	req := transport.EmbedRequest{Type: "llama", Path: path, Text: "first", Config: transport.Config{NumCtx: 4096}}
	for _, text := range []string{"first", "second"} {
		req.Text = text
		result, err := svc.Embed(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Vector) != 2 || result.Vector[0] != 0.25 || result.Vector[1] != 0.75 {
			t.Fatalf("embedding result = %v", result.Vector)
		}
	}
	if memory.calls != 1 || len(configs) != 2 || configs[0].NumGpuLayers != 5 || configs[1].NumGpuLayers != 5 || releases != 0 {
		t.Fatalf("stable placement: probes=%d configs=%+v releases=%d", memory.calls, configs, releases)
	}
	if configs[0].NumCtx != 4096 || configs[0].HotContextTokens != 0 || configs[0].PlannerEffectiveContext != 0 {
		t.Fatalf("embedding acquired generation context limits: %+v", configs[0])
	}
	if err := svc.ReleaseEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Embed(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if memory.calls != 2 || len(configs) != 3 || configs[2].NumGpuLayers != 1 || releases != 1 {
		t.Fatalf("placement after release: probes=%d configs=%+v releases=%d", memory.calls, configs, releases)
	}
}

func TestUnit_ServiceEmbeddingReleasesBeforeConfigOrPathReplacement(t *testing.T) {
	params := ggufParams{BlockCount: 4, ContextLength: 4096}
	first := writeTestModelProfile(t, params)
	second := filepath.Join(t.TempDir(), "second.gguf")
	if err := os.WriteFile(second, []byte("test model bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousInspect := inspectLlamaModel
	inspectLlamaModel = func(path string) (ggufParams, error) {
		if path == second {
			return params, nil
		}
		return previousInspect(path)
	}
	t.Cleanup(func() { inspectLlamaModel = previousInspect })
	policy := embeddingLifecyclePolicy(t)
	memory := &embeddingMemorySource{snapshots: []capacity.DeviceSnapshot{{Kind: "gpu", TotalBytes: 1000, FreeBytes: 100}}}
	var events []string
	setEmbeddingLifecycleHooks(t, func(_ context.Context, path string, cfg Config, _ string) ([]float64, error) {
		events = append(events, "embed:"+path)
		return []float64{1}, nil
	}, func() error { events = append(events, "release"); return nil })
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(memory), WithHostMemorySource(staticMemory(1000)))
	req := transport.EmbedRequest{Type: "llama", Path: first, Text: "query", Config: transport.Config{NumCtx: 1024}}
	if _, err := svc.Embed(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.Config.NumCtx = 2048
	if _, err := svc.Embed(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.Path = second
	if _, err := svc.Embed(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	want := []string{"embed:" + first, "release", "embed:" + first, "release", "embed:" + second}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if memory.calls != 3 {
		t.Fatalf("memory probes = %d, want 3 placements", memory.calls)
	}
}

func TestUnit_ServiceEmbeddingReportsPolicyAndProbeFailures(t *testing.T) {
	path := writeTestModelProfile(t, ggufParams{BlockCount: 4, ContextLength: 4096})
	policy := embeddingLifecyclePolicy(t)
	deviceErr, hostErr := errors.New("device probe failed"), errors.New("host probe failed")
	invalid := policy
	badFraction := 1.1
	invalid.MaxResidentFrac = &badFraction
	for _, tc := range []struct {
		name      string
		policy    capacity.Policy
		deviceErr error
		hostErr   error
		wantErr   error
		wantText  string
	}{
		{name: "device", policy: policy, deviceErr: deviceErr, wantErr: deviceErr},
		{name: "host", policy: policy, hostErr: hostErr, wantErr: hostErr},
		{name: "policy", policy: invalid, wantText: "--mem-max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			setEmbeddingLifecycleHooks(t, func(context.Context, string, Config, string) ([]float64, error) {
				calls++
				return []float64{1}, nil
			}, func() error { return nil })
			device := &embeddingMemorySource{snapshots: []capacity.DeviceSnapshot{{Kind: "gpu", TotalBytes: 1000, FreeBytes: 100}}, err: tc.deviceErr}
			host := &embeddingMemorySource{snapshots: []capacity.DeviceSnapshot{{Kind: "system", TotalBytes: 1000, FreeBytes: 100}}, err: tc.hostErr}
			svc := NewService(WithCapacityPolicy(tc.policy), WithMemorySource(device), WithHostMemorySource(host))
			_, err := svc.Embed(t.Context(), transport.EmbedRequest{Type: "llama", Path: path, Text: "query"})
			if err == nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) || tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("embedding error = %v, want %v %q", err, tc.wantErr, tc.wantText)
			}
			if calls != 0 {
				t.Fatalf("native embedding calls = %d, want 0 on failed resolution", calls)
			}
		})
	}
}

func TestUnit_ServiceEmbeddingPropagatesReleaseFailureBeforeReplacement(t *testing.T) {
	path := writeTestModelProfile(t, ggufParams{BlockCount: 4, ContextLength: 4096})
	policy := embeddingLifecyclePolicy(t)
	want := errors.New("embedding release failed")
	calls := 0
	setEmbeddingLifecycleHooks(t, func(context.Context, string, Config, string) ([]float64, error) {
		calls++
		return []float64{1}, nil
	}, func() error { return want })
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 1000, FreeBytes: 100}}), WithHostMemorySource(staticMemory(1000)))
	req := transport.EmbedRequest{Type: "llama", Path: path, Text: "query", Config: transport.Config{NumCtx: 1024}}
	if _, err := svc.Embed(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.Config.NumCtx = 2048
	if _, err := svc.Embed(t.Context(), req); !errors.Is(err, want) {
		t.Fatalf("replacement error = %v, want %v", err, want)
	}
	if err := svc.ReleaseEmbeddings(); !errors.Is(err, want) {
		t.Fatalf("release error = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("native calls = %d, want original only", calls)
	}
}

func TestUnit_ServiceEmbeddingUsesCPUForSystemMemory(t *testing.T) {
	path := writeTestModelProfile(t, ggufParams{BlockCount: 4, ContextLength: 4096})
	policy := embeddingLifecyclePolicy(t)
	gotLayers := -2
	setEmbeddingLifecycleHooks(t, func(_ context.Context, _ string, cfg Config, _ string) ([]float64, error) {
		gotLayers = cfg.NumGpuLayers
		return []float64{1}, nil
	}, func() error { return nil })
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "system", TotalBytes: 1000, FreeBytes: 100}}), WithHostMemorySource(staticMemory(1000)))
	if _, err := svc.Embed(t.Context(), transport.EmbedRequest{Type: "llama", Path: path, Text: "query", Config: transport.Config{NumGpuLayers: -1}}); err != nil {
		t.Fatal(err)
	}
	if gotLayers != 0 {
		t.Fatalf("system-memory embedding GPU layers = %d, want 0", gotLayers)
	}
}

func embeddingLifecyclePolicy(t *testing.T) capacity.Policy {
	t.Helper()
	for _, name := range []string{"CONTENOX_LLAMA_GPU_LAYERS", "CONTENOX_LLAMA_CTX", "CONTENOX_LLAMA_KV_CACHE_TYPE", "CONTENOX_LLAMA_GPU_COMPUTE_RESERVE"} {
		t.Setenv(name, "")
	}
	policy, err := capacity.DefaultPolicy().WithMemorySettings("50%", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	policy.HeadroomFrac = 0.1
	policy.ComputeReserve = "0%"
	return policy
}

func setEmbeddingLifecycleHooks(t *testing.T, embed EmbedFunc, release func() error) {
	t.Helper()
	previousEmbed := embedFunc
	embedReleaseMu.RLock()
	previousRelease := embedReleaseFunc
	embedReleaseMu.RUnlock()
	SetEmbedFunc(embed)
	SetEmbedReleaseFunc(release)
	t.Cleanup(func() {
		SetEmbedFunc(previousEmbed)
		SetEmbedReleaseFunc(previousRelease)
	})
}
