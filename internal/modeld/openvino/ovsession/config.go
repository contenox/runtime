package ovsession

// SparseAttentionEnabled reports the profile's explicit sparse-attention choice.
// An absent choice retains OpenVINO's dense-attention default.
func (cfg GenAIConfig) SparseAttentionEnabled() bool {
	return cfg.UseSparseAttention != nil && *cfg.UseSparseAttention
}
