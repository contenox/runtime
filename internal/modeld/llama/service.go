package llama

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/modeld/llama/llamacppshim"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
)

// Service implements the internal/transport.Service boundary.
// It acts as the opener for native llama.cpp backend sessions.
type Service struct {
	inspect    func(string) (ggufParams, error)
	memory     capacity.MemorySource
	hostMemory capacity.MemorySource
	policy     capacity.Policy
	tracker    libtracker.ActivityTracker
	embedMu    sync.Mutex
	embedPlan  *embeddingPlan
}

type ServiceOption func(*Service)

func NewService(opts ...ServiceOption) *Service {
	s := &Service{tracker: libtracker.NoopTracker{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithCapacityPolicy(p capacity.Policy) ServiceOption {
	return func(s *Service) { s.policy = p }
}

func WithMemorySource(src capacity.MemorySource) ServiceOption {
	return func(s *Service) { s.memory = src }
}

func WithHostMemorySource(src capacity.MemorySource) ServiceOption {
	return func(s *Service) { s.hostMemory = src }
}

// WithTracker sets the ActivityTracker session-open telemetry is reported
// through. Unset defaults to libtracker.NoopTracker.
func WithTracker(t libtracker.ActivityTracker) ServiceOption {
	return func(s *Service) {
		if t != nil {
			s.tracker = t
		}
	}
}

var _ transport.Service = (*Service)(nil)

// OpenSession binds a session to the requested model. It rejects a model typed
// for a different backend (ErrBackendMismatch) before loading, so a GGUF request
// sent to an openvino-mode daemon — or vice versa — fails at the boundary, not
// deep in the engine. The model is loaded from req.Path (resolved by the
// runtime); identity/caching uses req.Digest.
func (s *Service) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	return s.openSession(ctx, req, directAllocation)
}

func (s *Service) openSession(ctx context.Context, req transport.OpenSessionRequest, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.Session, error) {
	if req.Type != "" && req.Type != "llama" {
		return nil, fmt.Errorf("%w: requested %q, this daemon serves llama", transport.ErrBackendMismatch, req.Type)
	}
	plan, err := s.resolveSession(req)
	if err != nil {
		return nil, err
	}
	return s.openPlannedSession(ctx, req, plan, claim)
}

func (s *Service) openPlannedSession(ctx context.Context, req transport.OpenSessionRequest, plan sessionPlan, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.Session, error) {
	cfg := plan.config
	info := plan.info
	if cfg.ReasoningFormat == "" {
		cfg.ReasoningFormat = info.ChatTemplateReasoningFormat
	}
	tracker := s.tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	_, reportChange, end := tracker.Start(ctx, "modeld_llama", "open_session", "model", req.ModelName)
	defer end()
	reportChange("session_config", map[string]any{
		"num_ctx":                         cfg.NumCtx,
		"hot_context_tokens":              info.HotContextTokens,
		"planner_effective_context":       cfg.PlannerEffectiveContext,
		"host_cold_budget_bytes":          info.HostColdBudgetBytes,
		"num_batch":                       cfg.NumBatch,
		"num_gpu_layers":                  cfg.NumGpuLayers,
		"requested_gpu_layers":            info.RequestedGpuLayers,
		"resolved_gpu_layers":             info.ResolvedGpuLayers,
		"free_bytes":                      info.FreeBytes,
		"user_limit_bytes":                info.UserLimitBytes,
		"usable_bytes":                    info.UsableBytes,
		"weights_bytes":                   info.WeightsBytes,
		"overhead_bytes":                  info.OverheadBytes,
		"required_bytes":                  info.RequiredBytes,
		"capacity_reason":                 info.Reason,
		"flash_attention":                 cfg.FlashAttn,
		"kv_cache_type":                   cfg.KVCacheType,
		"sparse_attention":                info.SparseAttention,
		"sliding_window_attention_tokens": info.SlidingWindowAttentionTokens,
	})
	if err := claim(ctx, cfg, &info, fileSize(req.Path)); err != nil {
		return nil, err
	}
	session, err := newSession(req.Path, cfg, toAdapterSpecs(req.Adapters))
	if err != nil {
		return nil, err
	}
	return sessionkit.WithExecution(session, transport.SessionExecution{ModelName: req.ModelName, Digest: req.Digest, Backend: "llama", Requested: req.Config, Resolved: cfg, Capacity: info})
}

// toAdapterSpecs maps the transport adapter handles onto the backend-local
// AdapterSpec the session factory applies. The two types are kept distinct so the
// CGo session package never imports the wire shape; they carry the same fields.
func toAdapterSpecs(in []transport.AdapterSpec) []AdapterSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]AdapterSpec, len(in))
	for i, a := range in {
		out[i] = AdapterSpec{Name: a.Name, Path: a.Path, Digest: a.Digest, Scale: a.Scale}
	}
	return out
}

// Describe reports the model's trained context window read from the GGUF header
// (no tensor load). The runtime consumes this as the model's capacity; it never
// reads the GGUF itself.
func (s *Service) Describe(_ context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	if req.Type != "" && req.Type != "llama" {
		return transport.ModelInfo{}, fmt.Errorf("%w: requested %q, this daemon serves llama", transport.ErrBackendMismatch, req.Type)
	}
	info, err := s.describe(req)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	return info, nil
}

// Embed runs a one-shot native llama.cpp embedding for req.Text through the
// embedding backend registered by the CGo session package (see
// llamasession.embed). Like OpenVINO's Embed it is separate from OpenSession:
// embedding models do not use the chat session's prefix/suffix/Decode lifecycle.
// In a build without the native backend (no 'llamanode' tag) the embed func is
// unregistered and this reports ErrUnsupportedFeature.
func (s *Service) Embed(ctx context.Context, req transport.EmbedRequest) (transport.EmbedResult, error) {
	return s.embed(ctx, req, directAllocation)
}

func (s *Service) embed(ctx context.Context, req transport.EmbedRequest, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.EmbedResult, error) {
	if err := req.Config.Target.Validate(); err != nil {
		return transport.EmbedResult{}, err
	}
	if req.Type != "" && req.Type != "llama" {
		return transport.EmbedResult{}, fmt.Errorf("%w: requested %q, this daemon serves llama", transport.ErrBackendMismatch, req.Type)
	}
	if !EmbedAvailable() {
		return transport.EmbedResult{}, fmt.Errorf("%w: llama embeddings require a native build (-tags 'llamanode llamacpp_direct')", transport.ErrUnsupportedFeature)
	}
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	requested := applyDaemonEnvOverrides(req.Config)
	if s.embedPlan != nil && (s.embedPlan.path != req.Path || !reflect.DeepEqual(s.embedPlan.requested, requested)) {
		if err := ReleaseEmbeddings(); err != nil {
			return transport.EmbedResult{}, err
		}
		s.embedPlan = nil
	}
	if s.embedPlan == nil {
		resolved, info, err := s.resolveEmbedding(req.Path, requested)
		if err != nil {
			return transport.EmbedResult{}, err
		}
		s.embedPlan = &embeddingPlan{path: req.Path, requested: requested, resolved: resolved, info: info}
	}
	if err := claim(ctx, s.embedPlan.resolved, &s.embedPlan.info, fileSize(req.Path)); err != nil {
		return transport.EmbedResult{}, err
	}
	vec, err := newEmbed(ctx, req.Path, s.embedPlan.resolved, req.Text)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return transport.EmbedResult{Vector: out, Execution: transport.CopyExecutionReport(transport.SessionExecution{ModelName: req.ModelName, Digest: req.Digest, Backend: "llama", Requested: req.Config, Resolved: s.embedPlan.resolved, Capacity: s.embedPlan.info})}, nil
}

func (s *Service) resolveConfig(req transport.OpenSessionRequest) (transport.Config, error) {
	plan, err := s.resolveSession(req)
	if err != nil {
		return transport.Config{}, err
	}
	return plan.config, nil
}

func (s *Service) resolveSession(req transport.OpenSessionRequest) (sessionPlan, error) {
	plan, err := s.plan(req)
	if err != nil {
		return sessionPlan{}, err
	}
	cfg := plan.config
	info := plan.info
	if info.EffectiveContext <= 0 && info.Reason != "" {
		return sessionPlan{}, fmt.Errorf("%w: model %q %s — use a smaller model or quant, or free device memory (%s)",
			transport.ErrContextOverflow, req.ModelName, budgetShortfall(info), info.Reason)
	}
	if info.ResolvedGpuLayers <= 0 && isAcceleratorSnapshot(capacity.DeviceSnapshot{Kind: info.DeviceKind}) {
		return sessionPlan{}, fmt.Errorf("%w: no GPU layer fits in the selected %s memory budget (%s)",
			transport.ErrContextOverflow, info.DeviceKind, info.Reason)
	}
	if cfg.NumCtx <= 0 {
		cfg.NumCtx = info.HotContextTokens
		cfg.HotContextTokens = info.HotContextTokens
		cfg.PlannerEffectiveContext = transport.ResolvePlannerEffectiveContext(cfg.PlannerEffectiveContext, cfg.NumCtx, info)
		plan.config = cfg
		return plan, nil
	}
	if cfg.NumCtx > info.EffectiveContext {
		return sessionPlan{}, fmt.Errorf("%w: requested num_ctx=%d exceeds modeld effective context=%d (%s)",
			transport.ErrContextOverflow, cfg.NumCtx, info.EffectiveContext, info.Reason)
	}
	cfg.HotContextTokens = info.HotContextTokens
	cfg.PlannerEffectiveContext = transport.ResolvePlannerEffectiveContext(cfg.PlannerEffectiveContext, cfg.NumCtx, info)
	plan.config = cfg
	return plan, nil
}

// ReleaseEmbeddings releases native embedding weights after their last operation.
func (s *Service) ReleaseEmbeddings() error {
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	if err := ReleaseEmbeddings(); err != nil {
		return err
	}
	s.embedPlan = nil
	return nil
}

// budgetShortfall renders the memory arithmetic behind a no-spill refusal in
// operator units: what the model needs on the device (weights + KV + runtime
// overhead) versus the honest budget modeld will use after its device reserve.
// modeld refuses rather than shedding weights to host RAM, so the caller pairs
// this with the action that actually resolves it.
func budgetShortfall(info transport.ModelInfo) string {
	requiredKV := clampNonNegative(info.RequiredBytes - info.WeightsBytes - info.OverheadBytes)
	return fmt.Sprintf("needs ~%s on the %s device (weights %s + KV %s + overhead %s) but only %s is usable there",
		humanBytes(info.RequiredBytes), info.DeviceKind,
		humanBytes(info.WeightsBytes), humanBytes(requiredKV), humanBytes(info.OverheadBytes),
		humanBytes(info.UsableBytes))
}

// kvRoomBytes is how much of the usable device budget is left for KV cache once
// weights and runtime overhead are placed. Never negative.
func kvRoomBytes(info transport.ModelInfo) int64 {
	return clampNonNegative(info.UsableBytes - info.WeightsBytes - info.OverheadBytes)
}

func clampNonNegative(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// humanBytes formats a byte count in binary units for operator-facing messages.
func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func applyDaemonEnvOverrides(cfg transport.Config) transport.Config {
	cfg.Target = cfg.Target.Normalize()
	if v := os.Getenv("CONTENOX_LLAMA_GPU_LAYERS"); v != "" && cfg.Target.Mode == transport.PlacementAutomatic {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.NumGpuLayers = n
		}
	}
	if cfg.Target.Mode == transport.PlacementCPU {
		cfg.NumGpuLayers = 0
	}
	if v := os.Getenv("CONTENOX_LLAMA_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.NumCtx = n
		}
	}
	if v := os.Getenv("CONTENOX_LLAMA_KV_CACHE_TYPE"); v != "" {
		cfg.KVCacheType = v
	}
	return cfg
}

var defaultMemorySource = func(cfg transport.Config) capacity.MemorySource {
	if cfg.Target.Normalize().Mode == transport.PlacementAccelerator {
		return unavailableDeviceMemory{}
	}
	return capacity.SystemRAM{}
}

type unavailableDeviceMemory struct{}

func (unavailableDeviceMemory) FreeBytes() (int64, error) {
	return 0, errors.New("accelerator placement requires a native llama.cpp backend; " + remedy.PrepareLlama)
}

var llamaRuntimeInfo = func() transport.ModelInfo {
	return transport.ModelInfo{}
}

// RuntimeInfo reports the linked llama.cpp runtime identity and device
// inventory. In non-direct builds this returns an empty record.
func RuntimeInfo() transport.ModelInfo {
	return llamaRuntimeInfo()
}

// Set by Makefile builds so Describe can report the exact pinned llama.cpp
// source used for the direct runtime.
var llamaCPPCommit string

// BuildCommit returns the pinned llama.cpp source commit this backend was built
// against, as injected at link time. It is empty for a plain `go build` with no
// -ldflags. Cheap and side-effect free, so `modeld version` can report it without
// loading native libraries.
func BuildCommit() string { return llamaCPPCommit }

func (s *Service) memorySource(cfg transport.Config) capacity.MemorySource {
	if s.memory != nil {
		return s.memory
	}
	return defaultMemorySource(cfg)
}

func (s *Service) hostMemorySource() capacity.MemorySource {
	if s.hostMemory != nil {
		return s.hostMemory
	}
	return capacity.SystemRAM{}
}

func (s *Service) resolvePolicy(st capacity.DeviceSnapshot) (capacity.Policy, error) {
	host, err := capacity.Snapshot(s.hostMemorySource())
	if err != nil {
		return capacity.Policy{}, fmt.Errorf("llama host capacity memory probe: %w", err)
	}
	return capacity.ResolvePolicy(s.policy, st, host)
}

func (s *Service) describe(req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	if err := req.Config.Target.Validate(); err != nil {
		return transport.ModelInfo{}, err
	}
	cfg := applyDaemonEnvOverrides(req.Config)
	params, err := s.inspectModel(req.Path)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	st, err := capacity.Snapshot(s.memorySource(cfg))
	if err != nil {
		return transport.ModelInfo{}, fmt.Errorf("llama capacity memory probe: %w", err)
	}
	// Credit memory the slot owner says an eviction would reclaim (Describe-only
	// hint) before deriving the policy, so WithResidentDefault's live cap also
	// sees the post-switch picture.
	if req.ReclaimableBytes > 0 {
		st.FreeBytes += req.ReclaimableBytes
	}
	policy, err := s.resolvePolicy(st)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	policy.MinHotContextTokens = effectiveMinHotContext(policy.MinHotContextTokens, params.ContextLength)
	weights := fileSize(req.Path)
	layerKV := llamaLayerKVProfile(params, cfg.KVCacheType)
	kvBytes := layerKV.DenseKVBytesPerToken()
	// A vision model's projector loads whole (mtmd has no per-layer offload),
	// so its weights plus the encoder compute buffer enter the budget as fixed
	// overhead — never scaled by the GPU-layer walk-down like LLM weights.
	mmprojPath := MMProjPathFor(req.Path)
	overhead := int64(0)
	if mmprojPath != "" {
		overhead += fileSize(mmprojPath) + visionEncoderReserveBytes()
	}
	// modeld derives GPU offload from what it detected at runtime, not from a
	// per-model knob: with an accelerator present it offloads as many layers as
	// fit the VRAM budget; with none it runs on CPU (the CUDA plugin was silently
	// skipped). An explicit cfg.NumGpuLayers (model profile or
	// CONTENOX_LLAMA_GPU_LAYERS) is honored only as an upper cap.
	explicitGpuLayers := cfg.NumGpuLayers
	resolvedGpuLayers := 0
	if isAcceleratorSnapshot(st) {
		cfg.NumGpuLayers = autoGpuLayerCeiling(explicitGpuLayers)
		computeReserve, err := llamaGPUComputeReserveBytes(cfg, policy.ComputeReserve, st)
		if err != nil {
			return transport.ModelInfo{}, err
		}
		overhead += computeReserve
		resolvedGpuLayers = resolveGPULayersForBudget(explicitGpuLayers, cfg, params, layerKV, weights, kvBytes, overhead, st, policy)
		cfg.NumGpuLayers = resolvedGpuLayers
		weights = estimateLlamaGPUWeights(weights, params.BlockCount, cfg.NumGpuLayers)
	} else {
		cfg.NumGpuLayers = 0
	}
	resolved := capacity.Resolve(capacity.Params{
		ModelMaxCtx:         params.ContextLength,
		KVBytesPerToken:     kvBytes,
		LayerKV:             layerKV,
		WeightsBytes:        weights,
		OverheadBytes:       overhead,
		FreeBytes:           st.FreeBytes,
		UserLimitBytes:      policy.MaxResidentBytes,
		PreferredLimit:      policy.MaxResidentPreferred,
		MinFreeBytes:        policy.MinFreeBytes,
		HostColdBudgetBytes: policy.HostColdBudgetBytes,
		// cfg.NumCtx here must only ever carry a genuine explicit setting
		// (profile runtime.num_ctx / CONTENOX_LLAMA_CTX) or 0 for auto — never a
		// prior resolution's EffectiveContext; see capacity.HardContextLimit.
		Request:          capacity.HardContextLimit(cfg.NumCtx),
		HeadroomFrac:     policy.HeadroomFrac,
		HeadroomExplicit: policy.HeadroomExplicit,
	})
	info := modelInfo(resolved, st)
	info.SupportsEmbedding = EmbedAvailable() && (params.PoolingType > 0 || cfg.EmbeddingPooling != "")
	if params.SlidingWindow > 0 {
		info.SparseAttention = true
		info.SlidingWindowAttentionTokens = params.SlidingWindow
	}
	info.RequestedGpuLayers = explicitGpuLayers
	info.ResolvedGpuLayers = resolvedGpuLayers
	// Vision capability is certified from the projector's own metadata, never
	// inferred: no resolvable mmproj, or one that does not declare image
	// input, keeps SupportsVision false.
	if mmprojPath != "" {
		vision, _ := mmprojCaps(mmprojPath)
		info.SupportsVision = vision
		if vision {
			if profile, profileErr := inspectMMProjProfile(mmprojPath); profileErr == nil {
				info.VisionTokensPerImage = visionTokensPerImageEstimate(profile)
			}
		}
	}
	applyChatTemplateProbe(&info, req.Path)
	if explicitGpuLayers > 0 && resolvedGpuLayers < explicitGpuLayers {
		info.Clamped = true
		if info.Reason == "" {
			if isAcceleratorSnapshot(st) {
				info.Reason = "gpu_layers_exceed_memory_budget"
			} else {
				info.Reason = "no_accelerator_present"
			}
		}
	}
	return info, nil
}

// applyChatTemplateProbe fills the model's chat capabilities from its own
// template. The reasoning format starts at "auto" — extract whatever the
// template declares — because llama.cpp reads an unset format as no reasoning at
// all, which would deliver a thinking model's chain of thought as its answer. A
// probe that cannot run, or a template with no reasoning markers, must not turn
// that off by omission.
func applyChatTemplateProbe(info *transport.ModelInfo, modelPath string) {
	info.ChatTemplateReasoningFormat = "auto"
	probe, err := llamacppshim.ProbeChatTemplate(modelPath, ChatTemplateFor(modelPath))
	if err != nil {
		return
	}
	info.ChatTemplateFormat = probe.FormatName
	info.ChatTemplateThinkingStartTag = probe.ThinkingStartTag
	info.ChatTemplateSupportsToolCalls = probe.SupportsToolCalls
	info.ChatTemplateSupportsThinking = probe.SupportsThinking
	info.ChatTemplateSupportsReasoningEffort = probe.SupportsReasoningEffort
}

func llamaLayerKVProfile(params ggufParams, kvType string) capacity.LayerKVProfile {
	global, windowed := params.layerSplit()
	return capacity.LayerKVProfile{
		GlobalLayers:    global,
		WindowedLayers:  windowed,
		Window:          params.SlidingWindow,
		PerLayerKVBytes: capacity.KVBytesPerToken(1, params.kvHeads(), params.headDim(), kvType),
	}
}

func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return info.Size()
	}
	return 0
}

func modelInfo(c capacity.ModelCapacity, st capacity.DeviceSnapshot) transport.ModelInfo {
	info := llamaRuntimeInfo()
	info.ModelMaxContext = c.ModelMaxContext
	info.EffectiveContext = c.EffectiveContext
	info.MemoryContextTokens = c.MemoryContextTokens
	info.HotContextTokens = c.HotContextTokens
	info.PlannerEffectiveContext = c.PlannerEffectiveContext
	info.KVBytesPerToken = c.KVBytesPerToken
	info.FreeBytes = c.FreeBytes
	info.WeightsBytes = c.WeightsBytes
	info.OverheadBytes = c.OverheadBytes
	info.ReservedBytes = c.ReservedBytes
	info.UserLimitBytes = c.UserLimitBytes
	info.MinFreeBytes = c.MinFreeBytes
	info.HostColdBudgetBytes = c.HostColdBudgetBytes
	info.UsableBytes = c.UsableBytes
	info.RequiredBytes = c.RequiredBytes
	info.Clamped = c.Clamped
	info.Reason = c.Reason
	info.DeviceKind = st.Kind
	info.DeviceID = st.DeviceID
	info.DeviceTotalBytes = st.TotalBytes
	info.SharedWithDisplay = st.SharedWithDisplay
	if info.RuntimeDigest == "" {
		info.RuntimeDigest = llamaCPPCommit
	}
	return info
}

type sessionPlan struct {
	config transport.Config
	info   transport.ModelInfo
}

func (s *Service) plan(req transport.OpenSessionRequest) (sessionPlan, error) {
	cfg := applyDaemonEnvOverrides(req.Config)
	info, err := s.describe(transport.OpenSessionRequest{
		Fence:     req.Fence,
		ModelName: req.ModelName,
		Type:      req.Type,
		Digest:    req.Digest,
		Path:      req.Path,
		Config:    cfg,
	})
	if err != nil {
		return sessionPlan{}, err
	}
	// describe computes the authoritative offload count (0 on CPU / no accelerator,
	// the VRAM-fitted count on a detected accelerator), so it always wins over the
	// incoming request value.
	cfg.NumGpuLayers = info.ResolvedGpuLayers
	if len(cfg.TensorSplit) == 0 {
		if info.ResolvedGpuLayers == 0 {
			cfg.Target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
		} else if info.DeviceID != "" {
			cfg.Target = transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: info.DeviceID}
		}
	}
	return sessionPlan{config: cfg, info: info}, nil
}

const defaultLlamaGPUComputeReserveBytes int64 = 768 << 20

func llamaGPUComputeReserveBytes(cfg transport.Config, setting string, device capacity.DeviceSnapshot) (int64, error) {
	batch := cfg.NumBatch
	if batch <= 0 {
		batch = 512
	}
	reserve := defaultLlamaGPUComputeReserveBytes * int64(batch) / 512
	if reserve < 256<<20 {
		reserve = 256 << 20
	}
	if setting == "" {
		setting = os.Getenv("CONTENOX_LLAMA_GPU_COMPUTE_RESERVE")
	}
	if setting == "" {
		setting = "auto"
	}
	return capacity.ResolveComputeReserve(setting, reserve, device)
}

// allGpuLayers is the conventional llama.cpp "offload every layer" sentinel.
// resolveGPULayersForBudget caps it to the model's real layer count, so it just
// means "as many as fit"; it is large enough to exceed any real model's depth.
const allGpuLayers = 999

// DefaultMinHotContextTokens is the preferred context floor for automatic fitting.
// Tiny contexts can truncate system prompts; the floor guides offload fitting but
// does not reject a smaller usable allocation.
const DefaultMinHotContextTokens = 4096

func effectiveMinHotContext(configured, modelMaxCtx int) int {
	if configured < 0 {
		return 0
	}
	floor := configured
	if floor == 0 {
		floor = DefaultMinHotContextTokens
	}
	if modelMaxCtx > 0 && floor > modelMaxCtx {
		floor = modelMaxCtx
	}
	return floor
}

// autoGpuLayerCeiling is the offload ceiling modeld aims for once an accelerator
// is detected: an explicit cap when the caller set one (model profile or
// CONTENOX_LLAMA_GPU_LAYERS), otherwise all layers. resolveGPULayersForBudget
// then lowers it to what actually fits VRAM.
func autoGpuLayerCeiling(explicit int) int {
	if explicit > 0 {
		return explicit
	}
	return allGpuLayers
}

func resolveGPULayersForBudget(explicitGpuLayers int, cfg transport.Config, params ggufParams, layerKV capacity.LayerKVProfile, weights, kvBytes, overhead int64, st capacity.DeviceSnapshot, policy capacity.Policy) int {
	if cfg.NumGpuLayers <= 0 {
		return 0
	}
	if params.BlockCount <= 0 || weights <= 0 || kvBytes <= 0 {
		return cfg.NumGpuLayers
	}
	maxSlots := params.BlockCount
	if cfg.NumGpuLayers > params.BlockCount {
		maxSlots = params.BlockCount + 1 // output layer
	}
	requestedSlots := min(cfg.NumGpuLayers, maxSlots)
	fallbackSlots := 0
	for slots := requestedSlots; slots >= 1; slots-- {
		modelBytes := estimateLlamaGPUWeights(weights, params.BlockCount, slots)
		resolved := capacity.Resolve(capacity.Params{
			ModelMaxCtx:         params.ContextLength,
			KVBytesPerToken:     kvBytes,
			LayerKV:             layerKV,
			WeightsBytes:        modelBytes,
			OverheadBytes:       overhead,
			FreeBytes:           st.FreeBytes,
			UserLimitBytes:      policy.MaxResidentBytes,
			MinFreeBytes:        policy.MinFreeBytes,
			HostColdBudgetBytes: policy.HostColdBudgetBytes,
			Request:             capacity.HardContextLimit(cfg.NumCtx),
			HeadroomFrac:        policy.HeadroomFrac,
			HeadroomExplicit:    policy.HeadroomExplicit,
		})
		if resolved.EffectiveContext <= 0 {
			continue
		}
		if cfg.NumCtx > 0 && resolved.EffectiveContext < cfg.NumCtx {
			continue
		}
		if cfg.NumCtx <= 0 && policy.MinHotContextTokens > 0 && resolved.HotContextTokens < policy.MinHotContextTokens {
			if fallbackSlots == 0 {
				fallbackSlots = slots
			}
			continue
		}
		return slots
	}
	if fallbackSlots > 0 {
		return fallbackSlots
	}
	if policy.MaxResidentPreferred && policy.MaxResidentBytes > 0 {
		policy.MaxResidentBytes = 0
		return resolveGPULayersForBudget(explicitGpuLayers, cfg, params, layerKV, weights, kvBytes, overhead, st, policy)
	}
	return 0
}

func estimateLlamaGPUWeights(weights int64, blockCount, gpuLayers int) int64 {
	if weights <= 0 || gpuLayers <= 0 {
		return 0
	}
	if blockCount <= 0 {
		return weights
	}
	maxSlots := blockCount + 1 // repeating layers plus output layer
	slots := min(gpuLayers, maxSlots)
	perSlot := weights / int64(maxSlots)
	if perSlot <= 0 {
		return weights
	}
	est := perSlot * int64(slots)
	if est > weights {
		return weights
	}
	return est
}

func isAcceleratorSnapshot(st capacity.DeviceSnapshot) bool {
	return st.IsAccelerator()
}
