package stateservice

import (
	"context"
	"strings"

	"github.com/contenox/runtime/runtime/locale"
)

type RuntimeDefaults struct {
	ChainRef             string
	FIMChainRef          string
	Model                string
	Provider             string
	AltModel             string
	AltProvider          string
	AutocompleteModel    string
	AutocompleteProvider string
	MaxTokens            string
	Think                string
	Locale               string
}

func ResolveRuntimeDefaults(ctx context.Context, service Service, fallback RuntimeDefaults) RuntimeDefaults {
	if service == nil {
		return fallback.Trimmed()
	}
	snap, err := service.CLIConfig(ctx)
	if err != nil {
		return fallback.Trimmed()
	}
	return fallback.WithCLIConfig(snap).Trimmed()
}

func (d RuntimeDefaults) WithCLIConfig(snap CLIConfigSnapshot) RuntimeDefaults {
	if snap.has("default-chain", snap.DefaultChain) {
		d.ChainRef = snap.DefaultChain
	}
	if snap.has("default-model", snap.DefaultModel) {
		d.Model = snap.DefaultModel
	}
	if snap.has("default-provider", snap.DefaultProvider) {
		d.Provider = snap.DefaultProvider
	}
	if snap.has("default-alt-model", snap.DefaultAltModel) {
		d.AltModel = snap.DefaultAltModel
	}
	if snap.has("default-alt-provider", snap.DefaultAltProvider) {
		d.AltProvider = snap.DefaultAltProvider
	}
	if snap.has("default-autocomplete-model", snap.DefaultAutocompleteModel) {
		d.AutocompleteModel = snap.DefaultAutocompleteModel
	}
	if snap.has("default-autocomplete-provider", snap.DefaultAutocompleteProvider) {
		d.AutocompleteProvider = snap.DefaultAutocompleteProvider
	}
	if snap.has("default-max-tokens", snap.DefaultMaxTokens) {
		d.MaxTokens = snap.DefaultMaxTokens
	}
	if snap.has("default-think", snap.DefaultThink) {
		d.Think = snap.DefaultThink
	}
	if snap.Locale != "" {
		d.Locale = snap.Locale
	}
	return d
}

func (d RuntimeDefaults) EffectiveLocale() string {
	if v, ok := locale.Normalize(d.Locale); ok {
		return v
	}
	return locale.Detect()
}

func (d RuntimeDefaults) Trimmed() RuntimeDefaults {
	return RuntimeDefaults{
		ChainRef:             strings.TrimSpace(d.ChainRef),
		FIMChainRef:          strings.TrimSpace(d.FIMChainRef),
		Model:                strings.TrimSpace(d.Model),
		Provider:             strings.TrimSpace(d.Provider),
		AltModel:             strings.TrimSpace(d.AltModel),
		AltProvider:          strings.TrimSpace(d.AltProvider),
		AutocompleteModel:    strings.TrimSpace(d.AutocompleteModel),
		AutocompleteProvider: strings.TrimSpace(d.AutocompleteProvider),
		MaxTokens:            strings.TrimSpace(d.MaxTokens),
		Think:                strings.TrimSpace(d.Think),
		Locale:               strings.TrimSpace(d.Locale),
	}
}

func (d RuntimeDefaults) TemplateVars() map[string]string {
	d = d.Trimmed()
	vars := map[string]string{}
	if d.Model != "" {
		vars["model"] = d.Model
		vars["default_model"] = d.Model
	}
	if d.Provider != "" {
		vars["provider"] = d.Provider
		vars["default_provider"] = d.Provider
	}
	if d.AltModel != "" {
		vars["alt_model"] = d.AltModel
	}
	if d.AltProvider != "" {
		vars["alt_provider"] = d.AltProvider
	}
	if d.MaxTokens != "" {
		vars["max_tokens"] = d.MaxTokens
	}
	if d.Think != "" {
		vars["think"] = d.Think
	}
	vars["locale"] = d.EffectiveLocale()
	return vars
}

func (s CLIConfigSnapshot) has(key, value string) bool {
	if s.Present != nil {
		return s.Present[key]
	}
	return strings.TrimSpace(value) != ""
}
