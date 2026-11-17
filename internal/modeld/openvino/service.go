package openvino

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/internal/modeld/residency"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
)

// openvinoEvictionBlock aligns the derived cache-eviction sizes to OpenVINO's KV
// block granularity.
const openvinoEvictionBlock = 32

const (
	openvinoDefaultSchedulerCacheGiB    = 1
	openvinoMaxDerivedSchedulerCacheGiB = 4
	openvinoSchedulerCacheOverheadNum   = 5
	openvinoSchedulerCacheOverheadDen   = 4
)

// buildTokenizersPath is the build-time fallback path to the OpenVINO tokenizers extension
// (set via build-modeld -ldflags -X) for an in-place dev build whose libs live in
// the venv. OpenVINO GenAI loads that extension via OPENVINO_TOKENIZERS_PATH_GENAI.
var buildTokenizersPath string

// buildGenAIVersion is the pinned OpenVINO GenAI version this backend was built
// against, injected at link time (empty for a plain `go build`). It lets
// `modeld version` cross-check that a packaged binary matches its bundle manifest.
var buildGenAIVersion string

// BuildGenAIVersion returns the pinned OpenVINO GenAI version. Cheap and
// side-effect free, so `modeld version` can report it without loading native libs.
func BuildGenAIVersion() string { return buildGenAIVersion }

// tokenizersLibName is the extension file the bundle/venv provides.
func tokenizersLibName() string {
	if runtime.GOOS == "windows" {
		return "openvino_tokenizers.dll"
	}
	return "libopenvino_tokenizers.so"
}

// init points OpenVINO GenAI at the tokenizers extension without requiring the
// caller to set OPENVINO_TOKENIZERS_PATH_GENAI. It prefers a bundle next to the
// binary and falls back to the build-time venv path for local development.
func init() {
	if os.Getenv("OPENVINO_TOKENIZERS_PATH_GENAI") != "" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "modeld-libs", tokenizersLibName())
		if _, err := os.Stat(cand); err == nil {
			_ = os.Setenv("OPENVINO_TOKENIZERS_PATH_GENAI", cand)
			return
		}
	}
	if buildTokenizersPath != "" {
		_ = os.Setenv("OPENVINO_TOKENIZERS_PATH_GENAI", buildTokenizersPath)
	}
}

// Service implements the internal/transport.Service boundary for the OpenVINO
// GenAI backend. It opens persistent, manifest-keyed sessions on the owned
// device (CPU / GPU / NPU); the runtime reaches it as a client over the
// transport and never imports this package.
type Service struct {
	memory     capacity.MemorySource
	hostMemory capacity.MemorySource
	policy     capacity.Policy
	tracker    libtracker.ActivityTracker
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

func (s *Service) memorySource(device string) capacity.MemorySource {
	if s.memory != nil {
		return s.memory
	}
	device = effectiveDevice(device)
	if openvinoDeviceUsesSystemRAM(device) {
		return capacity.SystemRAM{}
	}
	return openvinoDeviceMemorySource{device: device}
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
		return capacity.Policy{}, fmt.Errorf("openvino host capacity memory probe: %w", err)
	}
	return capacity.ResolvePolicy(s.policy, st, host)
}

var _ transport.Service = (*Service)(nil)

var (
	inspectOpenVINOModel      = ovsession.InspectModelKVProfile
	probeOpenVINOChatTemplate = ovsession.ProbeChatTemplate
	newOpenVINOGenAI          = ovsession.NewGenAI
	newOpenVINOVLM            = func(modelDir, device string) (vlmBackend, error) {
		return ovsession.NewVLM(modelDir, device)
	}
)

// OpenSession makes the model at req.Path (an OpenVINO IR directory, resolved by
// the runtime) resident and returns a session bound to it. It rejects a model
// typed for a different backend (ErrBackendMismatch) before loading, so a request
// for a llama model on an openvino-mode daemon fails at the boundary. In a build
// without the openvino + openvino_genai tags, ovsession.NewGenAI reports the
// backend is not compiled in and that error surfaces here unchanged.
func (s *Service) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	return s.openSessions(ctx, req, directAllocation)
}

func (s *Service) openSessions(ctx context.Context, req transport.OpenSessionRequest, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.Session, error) {
	if req.Type != "" && req.Type != "openvino" {
		return nil, fmt.Errorf("%w: requested %q, this daemon serves openvino", transport.ErrBackendMismatch, req.Type)
	}
	config, err := configForTarget(req.Path, req.Config.Target)
	if err != nil {
		return nil, err
	}
	candidates := openSessionDevices(config.Device, devicePriority())
	var failures []error
	for _, device := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := config
		candidate.Device = device
		session, err := s.openClaimedSession(ctx, req, candidate, claim)
		if err == nil {
			return session, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", device, err))
	}
	return nil, fmt.Errorf("OpenVINO could not open the selected configuration; "+remedy.ReplanPlacement+": %w", errors.Join(failures...))
}

func (s *Service) openSession(ctx context.Context, req transport.OpenSessionRequest, genaiCfg ovsession.GenAIConfig) (transport.Session, error) {
	return s.openClaimedSession(ctx, req, genaiCfg, directAllocation)
}

func (s *Service) openClaimedSession(ctx context.Context, req transport.OpenSessionRequest, genaiCfg ovsession.GenAIConfig, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.Session, error) {
	if openvinoDeviceBase(genaiCfg.Device) == "NPU" {
		return nil, fmt.Errorf("%w: OpenVINO NPU cannot run the continuous-batching or vision pipeline because PagedAttention is unsupported; "+remedy.ReplanPlacement, transport.ErrUnsupportedFeature)
	}
	info, err := s.describeConfigured(req, genaiCfg)
	if err != nil {
		return nil, err
	}
	cfg, err := resolveConfigFromInfo(req.Config, info)
	if err != nil {
		return nil, err
	}
	cfg.Target = resolvedTarget(genaiCfg.Device, info.DeviceID)
	if err := claim(ctx, cfg, &info, dirSize(req.Path)); err != nil {
		return nil, err
	}
	_, reportChange, end := s.tracker.Start(ctx, "modeld_openvino", "open_session", "model", req.ModelName)
	defer end()
	reportChange("device_selected", map[string]any{"model": req.ModelName, "device": genaiCfg.Device, "device_id": info.DeviceID})
	if isVLMModelDir(req.Path) {
		if len(req.Adapters) > 0 {
			return nil, fmt.Errorf("%w: openvino VLM session does not support LoRA adapters", transport.ErrUnsupportedFeature)
		}
		backend, err := newOpenVINOVLM(req.Path, genaiCfg.Device)
		if err != nil {
			return nil, err
		}
		session := newVisionSession(backend, cfg.NumCtx, info.VisionTokensPerImage)
		session.execution = transport.CopyExecutionReport(transport.SessionExecution{ModelName: req.ModelName, Digest: req.Digest, Backend: "openvino", DeviceSelector: genaiCfg.Device, Requested: req.Config, Resolved: cfg, Capacity: info})
		return session, nil
	}
	applyOpenVINOSchedulerCacheDefault(&genaiCfg, info, cfg.HotContextTokens)
	hotContext := cfg.HotContextTokens
	if hotContext <= 0 {
		hotContext = cfg.NumCtx
	}
	budget := residency.DeriveEvictionBudget(hotContext, info.SlidingWindowAttentionTokens, openvinoEvictionBlock)
	eviction := budget.Valid()
	if eviction {
		on := true
		genaiCfg.UseCacheEviction = &on
		genaiCfg.CacheEvictStartSize = budget.SinkTokens
		genaiCfg.CacheEvictRecentSize = budget.RecentTokens
		genaiCfg.CacheEvictMaxSize = budget.MaxTokens
	}
	genaiCfg.LoRAAdapters = toGenAILoRA(req.Adapters)
	backend, genaiCfg, err := openGenAIWithSparseFallback(reportChange, req.Path, req.ModelName, genaiCfg)
	if err != nil {
		return nil, err
	}
	sparseAttention := genaiCfg.SparseAttentionEnabled()
	protocols, err := parserProtocolsFromProfile(req.Path)
	if err != nil {
		backend.Close()
		return nil, fmt.Errorf("openvino parser profile: %w", err)
	}
	session := newGenaiSessionWithNativeFeatures(backend, cfg.NumCtx, cfg.PlannerEffectiveContext, eviction, sparseAttention, info.SlidingWindowAttentionTokens)
	session.execution = transport.CopyExecutionReport(transport.SessionExecution{ModelName: req.ModelName, Digest: req.Digest, Backend: "openvino", DeviceSelector: genaiCfg.Device, Requested: req.Config, Resolved: cfg, Capacity: info})
	session.parserProtocols = protocols
	session.deferPhysicalPrefill = openvinoDeferPhysicalPrefill()
	session.coldKVLossless = kvPrecisionLossless(genaiCfg.KVCachePrecision)
	if !session.coldKVLossless && session.coldMaxTokens > 0 {
		reportChange("cold_kv_disabled", map[string]any{
			"model": req.ModelName, "kv_cache_precision": genaiCfg.KVCachePrecision, "cold_max_tokens": session.coldMaxTokens,
			"hint": "set kv_cache_precision to f16 or f32 to enable cold KV / effective-context offload",
		})
	}
	return session, nil
}

func openGenAIWithSparseFallback(reportChange func(string, any), modelPath, modelName string, cfg ovsession.GenAIConfig) (*ovsession.GenAISession, ovsession.GenAIConfig, error) {
	backend, err := newOpenVINOGenAI(modelPath, cfg)
	if err == nil {
		return backend, cfg, nil
	}
	if cfg.UseSparseAttention != nil || !openvinoXAttentionUnsupported(err) {
		return nil, cfg, err
	}
	denseCfg := cfg
	off := false
	denseCfg.UseSparseAttention = &off
	backend, denseErr := newOpenVINOGenAI(modelPath, denseCfg)
	if denseErr != nil {
		return nil, cfg, fmt.Errorf("%w; dense fallback after unsupported XAttention also failed: %v", err, denseErr)
	}
	reportChange("xattention_unsupported_dense_fallback", map[string]any{"model": modelName, "device": cfg.Device, "error": err.Error()})
	return backend, denseCfg, nil
}

func openvinoXAttentionUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "xattention") &&
		(strings.Contains(msg, "not supported") ||
			strings.Contains(msg, "unsupported") ||
			strings.Contains(msg, "not available"))
}

func openvinoDeferPhysicalPrefill() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("CONTENOX_OPENVINO_DEFER_PREFILL")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func applyOpenVINOSchedulerCacheDefault(cfg *ovsession.GenAIConfig, info transport.ModelInfo, hotContext int) {
	if cfg == nil || cfg.CacheSizeExplicit || cfg.CacheSize > 0 {
		return
	}
	cfg.CacheSize = deriveOpenVINOSchedulerCacheSizeGiB(info, hotContext)
}

func deriveOpenVINOSchedulerCacheSizeGiB(info transport.ModelInfo, hotContext int) int {
	if hotContext <= 0 {
		hotContext = info.HotContextTokens
	}
	if hotContext <= 0 || info.KVBytesPerToken <= 0 {
		return openvinoDefaultSchedulerCacheGiB
	}
	const gib = int64(1 << 30)
	kvBytes := int64(hotContext) * info.KVBytesPerToken
	if kvBytes <= 0 {
		return openvinoDefaultSchedulerCacheGiB
	}
	withOverhead := kvBytes * openvinoSchedulerCacheOverheadNum / openvinoSchedulerCacheOverheadDen
	cacheGiB := int((withOverhead + gib - 1) / gib)
	if cacheGiB < openvinoDefaultSchedulerCacheGiB {
		return openvinoDefaultSchedulerCacheGiB
	}
	if cacheGiB > openvinoMaxDerivedSchedulerCacheGiB {
		return openvinoMaxDerivedSchedulerCacheGiB
	}
	return cacheGiB
}

// toGenAILoRA maps the transport adapter handles onto OpenVINO's safetensors
// adapter config. The transport Scale becomes OpenVINO's folded alpha; the backend
// decides rank normalization. Empty in → nil (base model).
func toGenAILoRA(in []transport.AdapterSpec) []ovsession.GenAILoRAAdapter {
	if len(in) == 0 {
		return nil
	}
	out := make([]ovsession.GenAILoRAAdapter, len(in))
	for i, a := range in {
		out[i] = ovsession.GenAILoRAAdapter{Path: a.Path, Alpha: a.Scale}
	}
	return out
}

// Describe reports the model's trained context window read from the IR's
// config.json (max_position_embeddings) — no pipeline load. The runtime consumes
// this as the model's capacity; it never reads the IR files itself.
func (s *Service) Describe(_ context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	if req.Type != "" && req.Type != "openvino" {
		return transport.ModelInfo{}, fmt.Errorf("%w: requested %q, this daemon serves openvino", transport.ErrBackendMismatch, req.Type)
	}
	info, err := s.describe(req)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	return info, nil
}

// Embed runs a one-shot OpenVINO GenAI TextEmbeddingPipeline for req.Text. It is
// deliberately separate from OpenSession: embedding models do not use the chat
// session's prefix/suffix/Decode lifecycle.
func (s *Service) Embed(ctx context.Context, req transport.EmbedRequest) (transport.EmbedResult, error) {
	return s.embed(ctx, req, directAllocation)
}

func (s *Service) embed(ctx context.Context, req transport.EmbedRequest, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.EmbedResult, error) {
	if req.Type != "" && req.Type != "openvino" {
		return transport.EmbedResult{}, fmt.Errorf("%w: requested %q, this daemon serves openvino", transport.ErrBackendMismatch, req.Type)
	}
	genaiCfg, err := configForTarget(req.Path, req.Config.Target)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	var failures []error
	for _, device := range openSessionDevices(genaiCfg.Device, devicePriority()) {
		if err := ctx.Err(); err != nil {
			return transport.EmbedResult{}, err
		}
		snapshot, err := capacity.Snapshot(s.memorySource(device))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", device, err))
			continue
		}
		resolved := req.Config
		resolved.Target = resolvedTarget(device, snapshot.DeviceID)
		weights := dirSize(req.Path)
		info := transport.ModelInfo{DeviceKind: snapshot.Kind, DeviceID: snapshot.DeviceID, WeightsBytes: weights, RequiredBytes: weights}
		if err := claim(ctx, resolved, &info, weights); err != nil {
			return transport.EmbedResult{}, err
		}
		backend, err := newEmbedSession(req.Path, device)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", device, err))
			continue
		}
		vec, err := backend.Embed(ctx, req.Text)
		if err = errors.Join(err, backend.Close()); err != nil {
			return transport.EmbedResult{}, err
		}
		return transport.EmbedResult{Vector: vec, Execution: transport.CopyExecutionReport(transport.SessionExecution{ModelName: req.ModelName, Digest: req.Digest, Backend: "openvino", DeviceSelector: device, Requested: req.Config, Resolved: resolved, Capacity: info})}, nil
	}
	return transport.EmbedResult{}, fmt.Errorf("OpenVINO embedding could not open the selected device: %w", errors.Join(failures...))
}

type openvinoParams struct {
	MaxPositionEmbeddings int
	NumHiddenLayers       int
	NumKeyValueHeads      int
	NumAttentionHeads     int
	HiddenSize            int
	HeadDim               int
	SlidingWindow         int
	GlobalLayers          int
	WindowedLayers        int
}

func (p openvinoParams) kvHeads() int {
	if p.NumKeyValueHeads > 0 {
		return p.NumKeyValueHeads
	}
	return p.NumAttentionHeads
}

func (p openvinoParams) headDim() int {
	if p.HeadDim > 0 {
		return p.HeadDim
	}
	if p.HiddenSize > 0 && p.NumAttentionHeads > 0 {
		return p.HiddenSize / p.NumAttentionHeads
	}
	return 0
}

func (p openvinoParams) layerKVProfile(kvPrecision string) capacity.LayerKVProfile {
	return capacity.LayerKVProfile{
		GlobalLayers:    p.GlobalLayers,
		WindowedLayers:  p.WindowedLayers,
		Window:          p.SlidingWindow,
		PerLayerKVBytes: capacity.KVBytesPerToken(1, p.kvHeads(), p.headDim(), kvPrecision),
	}
}

func openvinoModelParams(modelDir string) (openvinoParams, error) {
	profile, err := inspectOpenVINOModel(modelDir)
	if err != nil {
		return openvinoParams{}, fmt.Errorf("openvino model KV profile: %w", err)
	}
	return openvinoParams{
		MaxPositionEmbeddings: profile.MaxPositionEmbeddings,
		NumHiddenLayers:       profile.NumHiddenLayers,
		NumKeyValueHeads:      profile.NumKeyValueHeads,
		NumAttentionHeads:     profile.NumAttentionHeads,
		HiddenSize:            profile.HiddenSize,
		HeadDim:               profile.HeadDim,
		SlidingWindow:         profile.SlidingWindow,
		GlobalLayers:          profile.GlobalLayers,
		WindowedLayers:        profile.WindowedLayers,
	}, nil
}

// ContextLength returns an OpenVINO IR model's trained context ceiling
// (max_position_embeddings) via the same header-only native inspect
// openvinoModelParams uses — no GenAI pipeline load, no device query.
// Callers (e.g. modelstore.Admin.ListModels) should treat an error as
// "unknown" and skip enrichment for that one model, not fail the scan.
func ContextLength(modelDir string) (int, error) {
	p, err := openvinoModelParams(modelDir)
	if err != nil {
		return 0, err
	}
	return p.MaxPositionEmbeddings, nil
}

func (s *Service) resolveConfig(req transport.OpenSessionRequest) (transport.Config, error) {
	info, err := s.describe(req)
	if err != nil {
		return transport.Config{}, err
	}
	return resolveConfigFromInfo(req.Config, info)
}

func resolveConfigFromInfo(cfg transport.Config, info transport.ModelInfo) (transport.Config, error) {
	if info.EffectiveContext <= 0 {
		return cfg, nil
	}
	if cfg.NumCtx <= 0 {
		cfg.NumCtx = info.HotContextTokens
		cfg.HotContextTokens = info.HotContextTokens
		cfg.PlannerEffectiveContext = transport.ResolvePlannerEffectiveContext(cfg.PlannerEffectiveContext, cfg.NumCtx, info)
		return cfg, nil
	}
	if cfg.NumCtx > info.EffectiveContext {
		return transport.Config{}, fmt.Errorf("%w: requested num_ctx=%d exceeds modeld effective context=%d (%s)",
			transport.ErrContextOverflow, cfg.NumCtx, info.EffectiveContext, info.Reason)
	}
	cfg.HotContextTokens = info.HotContextTokens
	cfg.PlannerEffectiveContext = transport.ResolvePlannerEffectiveContext(cfg.PlannerEffectiveContext, cfg.NumCtx, info)
	return cfg, nil
}

func (s *Service) describe(req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	genai, err := configForTarget(req.Path, req.Config.Target)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	return s.describeConfigured(req, genai)
}

func (s *Service) describeConfigured(req transport.OpenSessionRequest, genai ovsession.GenAIConfig) (transport.ModelInfo, error) {
	params, err := openvinoModelParams(req.Path)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	device := genai.Device
	st, err := capacity.Snapshot(s.memorySource(device))
	if err != nil {
		return transport.ModelInfo{}, fmt.Errorf("openvino capacity memory probe: %w", err)
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
	layerKV := params.layerKVProfile(genai.KVCachePrecision)
	kvBytes := layerKV.DenseKVBytesPerToken()
	resolved := capacity.Resolve(capacity.Params{
		ModelMaxCtx:         params.MaxPositionEmbeddings,
		KVBytesPerToken:     kvBytes,
		LayerKV:             layerKV,
		WeightsBytes:        dirSize(req.Path),
		FreeBytes:           st.FreeBytes,
		UserLimitBytes:      policy.MaxResidentBytes,
		PreferredLimit:      policy.MaxResidentPreferred,
		MinFreeBytes:        policy.MinFreeBytes,
		HostColdBudgetBytes: policy.HostColdBudgetBytes,
		// req.Config.NumCtx must only carry a genuine explicit setting or 0 for
		// auto — never a prior resolution's EffectiveContext; see
		// capacity.HardContextLimit.
		Request:          capacity.HardContextLimit(req.Config.NumCtx),
		HeadroomFrac:     policy.HeadroomFrac,
		HeadroomExplicit: policy.HeadroomExplicit,
	})
	info := modelInfo(resolved, st)
	if params.SlidingWindow > 0 {
		info.SparseAttention = true
		info.SlidingWindowAttentionTokens = params.SlidingWindow
	}
	// Vision capability is certified from the export's own IR layout (language
	// model + vision embeddings present), never inferred from the model name.
	// Weights already count the vision/text-embedding IRs because dirSize sums
	// the whole directory; the per-image token estimate comes from config.json
	// (0 = unknown, see visionTokensPerImageEstimate).
	if isVLMModelDir(req.Path) {
		info.SupportsVision = true
		info.VisionTokensPerImage = visionTokensPerImageEstimate(req.Path)
	}
	applyOpenVINOChatTemplateProbe(&info, req.Path)
	return info, nil
}

func applyOpenVINOChatTemplateProbe(info *transport.ModelInfo, modelPath string) {
	probe, err := probeOpenVINOChatTemplate(modelPath)
	if err != nil {
		return
	}
	info.ChatTemplateFormat = probe.FormatName
	info.ChatTemplateThinkingStartTag = probe.ThinkingStartTag
	info.ChatTemplateSupportsToolCalls = probe.SupportsToolCalls
	info.ChatTemplateSupportsThinking = probe.SupportsThinking
	info.ChatTemplateSupportsReasoningEffort = probe.SupportsReasoningEffort
	if probe.SupportsThinking || probe.SupportsReasoningEffort || probe.ThinkingStartTag != "" {
		info.ChatTemplateReasoningFormat = "auto"
	}
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func modelInfo(c capacity.ModelCapacity, st capacity.DeviceSnapshot) transport.ModelInfo {
	info := openvinoRuntimeInfo()
	info.SupportsEmbedding = ovsession.EmbedAvailable()
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
	return info
}

func openvinoRuntimeInfo() transport.ModelInfo {
	info := transport.ModelInfo{RuntimeName: "OpenVINO GenAI"}
	rt, err := ovsession.Runtime()
	if err != nil {
		info.RuntimeError = err.Error()
		return info
	}
	info.RuntimeName = rt.RuntimeName
	info.RuntimeDigest = rt.RuntimeDigest
	info.RuntimeSystemInfo = rt.RuntimeSystemInfo
	info.SupportsGPUOffload = rt.SupportsGPUOffload
	info.Devices = make([]transport.DeviceInfo, 0, len(rt.Devices))
	for _, d := range rt.Devices {
		info.Devices = append(info.Devices, transport.DeviceInfo{
			ID:               d.ID,
			SharedMemory:     d.Type == "igpu" || d.Type == "cpu",
			Index:            d.Index,
			Name:             d.Name,
			Description:      d.Description,
			Type:             d.Type,
			MemoryFree:       int64(d.MemoryFree),
			MemoryTotal:      int64(d.MemoryTotal),
			MemoryFreeKnown:  d.MemoryFreeKnown,
			MemoryTotalKnown: d.MemoryTotalKnown,
		})
	}
	return info
}

// RuntimeInfo reports the linked OpenVINO runtime identity and device inventory.
// In builds without the native OpenVINO GenAI tags, this returns a minimal
// record with RuntimeName set.
func RuntimeInfo() transport.ModelInfo {
	return openvinoRuntimeInfo()
}

type openvinoDeviceMemorySource struct {
	device string
}

func (s openvinoDeviceMemorySource) FreeBytes() (int64, error) {
	st, err := s.Snapshot()
	if err != nil {
		return 0, err
	}
	return st.FreeBytes, nil
}

func (s openvinoDeviceMemorySource) Snapshot() (capacity.DeviceSnapshot, error) {
	d, err := ovsession.Device(s.device)
	if err != nil {
		return capacity.DeviceSnapshot{}, err
	}
	return openvinoDeviceSnapshot(d, capacity.SystemRAM{})
}

func openvinoDeviceSnapshot(d ovsession.DeviceInfo, host capacity.MemorySource) (capacity.DeviceSnapshot, error) {
	kind := d.Type
	if kind == "" {
		kind = openvinoDeviceKind(d.Name)
	}
	totalKnown, freeKnown := d.MemoryTotalKnown, d.MemoryFreeKnown
	total, free := int64(d.MemoryTotal), int64(d.MemoryFree)
	if d.SharedWithDisplay && (!freeKnown || free <= 0 || !totalKnown || total <= 0) {
		hostSnapshot, err := capacity.Snapshot(host)
		if err != nil {
			return capacity.DeviceSnapshot{}, fmt.Errorf("OpenVINO shared-memory device %q host memory fallback: %w", d.Name, err)
		}
		if !totalKnown || total <= 0 {
			total = hostSnapshot.TotalBytes
			totalKnown = total > 0
		}
		if !freeKnown || free <= 0 {
			free = hostSnapshot.FreeBytes
			if total > 0 && free > total {
				free = total
			}
			freeKnown = free > 0
		}
	}
	if !freeKnown || !totalKnown || total <= 0 {
		return capacity.DeviceSnapshot{}, fmt.Errorf("OpenVINO device %q reported no memory telemetry; set CONTENOX_OPENVINO_DEVICE=CPU or use a plugin exposing device memory", d.Name)
	}
	identity := d.ID
	if identity == "" {
		identity = d.Name
	}
	return capacity.DeviceSnapshot{
		Kind:              kind,
		DeviceID:          identity,
		TotalBytes:        total,
		FreeBytes:         free,
		SharedWithDisplay: d.SharedWithDisplay,
	}, nil
}

func openvinoDeviceUsesSystemRAM(device string) bool {
	base := openvinoDeviceBase(device)
	return base == "" || base == "CPU"
}

// HasAccelerator reports whether OpenVINO can serve an accelerator on this host:
// a GPU or integrated GPU this runtime can budget. It is deliberately stricter
// than "enumerated a non-CPU device", because the daemon prefers OpenVINO over
// llama.cpp whenever this is true and a device it cannot place work on would make
// that preference wrong. The devices it rejects are the ones a session would
// reject: an NPU cannot compile the ContinuousBatchingPipeline, and a GPU whose
// memory cannot be budgeted fails every capacity probe — OpenVINO enumerates
// non-Intel GPUs through OpenCL without memory telemetry.
func HasAccelerator() bool {
	info, err := ovsession.Runtime()
	if err != nil {
		return false
	}
	for _, d := range info.Devices {
		if openvinoServesAccelerator(d) {
			return true
		}
	}
	return false
}

func openvinoDeviceKind(device string) string {
	switch openvinoDeviceBase(device) {
	case "GPU":
		return "gpu"
	case "NPU":
		return "accel"
	case "CPU":
		return "cpu"
	default:
		return "unknown"
	}
}

func openvinoDeviceBase(device string) string {
	device = strings.ToUpper(strings.TrimSpace(device))
	if i := strings.IndexByte(device, '.'); i >= 0 {
		device = device[:i]
	}
	if i := strings.IndexByte(device, ':'); i >= 0 {
		device = device[:i]
	}
	return device
}

// resolveDevice selects the OpenVINO inference device. CONTENOX_OPENVINO_DEVICE
// is the explicit override (set it to CPU/GPU/NPU to pin a device); the test
// device hint and an AUTO default follow. AUTO is OpenVINO's virtual plugin that
// places work on the best available device and falls back to CPU.
func resolveDevice() string {
	if device := os.Getenv("CONTENOX_OPENVINO_DEVICE"); device != "" {
		return device
	}
	if device := os.Getenv("CONTENOX_OPENVINO_TEST_DEVICE"); device != "" {
		return device
	}
	return "AUTO"
}

// effectiveDevice resolves a device selector to the concrete device that
// capacity planning should budget against. AUTO does not expose memory
// telemetry, so mirror the inference priority against the enumerated devices and
// budget against the device inference will actually land on. Concrete selectors
// pass through unchanged; if enumeration fails we fall back to CPU so capacity
// uses system RAM rather than failing the request.
func effectiveDevice(device string) string {
	if openvinoDeviceBase(device) != "AUTO" {
		return device
	}
	// Budget against the same device the pipeline will land on: mirror the
	// inference priority order so capacity planning and device selection agree.
	candidates := openSessionDevices("AUTO", devicePriority())
	if len(candidates) == 0 {
		return "CPU"
	}
	return candidates[0]
}
