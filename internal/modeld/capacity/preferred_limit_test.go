package capacity_test

import (
	"github.com/contenox/runtime/internal/modeld/capacity"
)

import "testing"

func TestUnit_Resolve_PreferredResidentSettingsAllowPhysicalFit(t *testing.T) {
	for _, setting := range []string{"30%", "auto"} {
		t.Run(setting, func(t *testing.T) {
			policy, err := capacity.DefaultPolicy().WithMemorySettings(setting, "0%", "0%")
			if err != nil {
				t.Fatal(err)
			}
			device := capacity.DeviceSnapshot{Kind: "system", TotalBytes: 1000, FreeBytes: 100}
			policy = capacity.WithResidentDefault(policy, device)
			if !policy.MaxResidentPreferred {
				t.Fatal("percentage or automatic resident setting lost preferred provenance")
			}
			resolved := capacity.Resolve(capacity.Params{
				ModelMaxCtx:      20,
				KVBytesPerToken:  5,
				WeightsBytes:     50,
				FreeBytes:        device.FreeBytes,
				UserLimitBytes:   policy.MaxResidentBytes,
				Request:          10,
				HeadroomExplicit: true,
				PreferredLimit:   policy.MaxResidentPreferred,
			})
			if resolved.EffectiveContext != 10 || resolved.UsableBytes != 100 || resolved.Reason != "resident_target_exceeded" {
				t.Fatalf("preferred resolution = %+v, want physical fit of 10 tokens", resolved)
			}
			if resolved.UserLimitBytes != policy.MaxResidentBytes {
				t.Fatalf("reported target = %d, want original %d", resolved.UserLimitBytes, policy.MaxResidentBytes)
			}
		})
	}
}

func TestUnit_Resolve_HardByteResidentSettingRemainsStrict(t *testing.T) {
	policy, err := capacity.DefaultPolicy().WithMemorySettings("30B", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	policy = capacity.WithResidentDefault(policy, capacity.DeviceSnapshot{Kind: "system", TotalBytes: 1000, FreeBytes: 100})
	if policy.MaxResidentPreferred {
		t.Fatal("byte cap became preferred")
	}
	resolved := capacity.Resolve(capacity.Params{
		ModelMaxCtx:      20,
		KVBytesPerToken:  5,
		WeightsBytes:     50,
		FreeBytes:        100,
		UserLimitBytes:   policy.MaxResidentBytes,
		Request:          10,
		HeadroomExplicit: true,
		PreferredLimit:   policy.MaxResidentPreferred,
	})
	if resolved.EffectiveContext != 0 || resolved.UsableBytes != 30 || resolved.Reason != "request_exceeds_user_limit" {
		t.Fatalf("hard byte resolution = %+v, want strict 30-byte cap", resolved)
	}
}

func TestUnit_Resolve_PreferredTargetCannotInventPhysicalMemory(t *testing.T) {
	resolved := capacity.Resolve(capacity.Params{
		ModelMaxCtx:      20,
		KVBytesPerToken:  5,
		WeightsBytes:     50,
		FreeBytes:        90,
		UserLimitBytes:   30,
		Request:          10,
		HeadroomExplicit: true,
		PreferredLimit:   true,
	})
	if resolved.EffectiveContext != 8 || !resolved.Clamped || resolved.Reason != "request_exceeds_memory_budget" || resolved.UsableBytes != 90 {
		t.Fatalf("physical shortfall = %+v, want eight tokens and physical failure reason", resolved)
	}
	resolved = capacity.Resolve(capacity.Params{
		ModelMaxCtx:      20,
		KVBytesPerToken:  1,
		WeightsBytes:     100,
		FreeBytes:        100,
		UserLimitBytes:   30,
		Request:          1,
		HeadroomExplicit: true,
		PreferredLimit:   true,
	})
	if resolved.EffectiveContext != 0 || resolved.Reason != "request_exceeds_memory_budget" {
		t.Fatalf("exhausted physical pool = %+v, want no usable context", resolved)
	}
}

func TestUnit_Resolve_PreferredTargetIsUsedWhenItFits(t *testing.T) {
	resolved := capacity.Resolve(capacity.Params{
		ModelMaxCtx:      20,
		KVBytesPerToken:  5,
		WeightsBytes:     10,
		FreeBytes:        100,
		UserLimitBytes:   50,
		Request:          5,
		HeadroomExplicit: true,
		PreferredLimit:   true,
	})
	if resolved.EffectiveContext != 5 || resolved.UsableBytes != 50 || resolved.Reason != "" {
		t.Fatalf("target fit = %+v, want target allocation without physical expansion", resolved)
	}
}

func TestUnit_Policy_ResidentOverrideReplacesPreferredProvenance(t *testing.T) {
	policy, err := capacity.DefaultPolicy().WithMemorySettings("30%", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	device := capacity.DeviceSnapshot{Kind: "system", TotalBytes: 1000, FreeBytes: 100}
	policy = capacity.WithResidentDefault(policy, device)
	policy, err = policy.WithMemorySettings("30B", "", "")
	if err != nil {
		t.Fatal(err)
	}
	policy = capacity.WithResidentDefault(policy, device)
	if policy.MaxResidentPreferred || policy.MaxResidentFrac != nil || policy.MaxResidentBytes != 30 {
		t.Fatalf("byte override retained preferred provenance: %+v", policy)
	}
	policy, err = policy.WithMemorySettings("auto", "", "")
	if err != nil {
		t.Fatal(err)
	}
	policy = capacity.WithResidentDefault(policy, device)
	if !policy.MaxResidentPreferred || policy.MaxResidentFrac == nil {
		t.Fatalf("automatic override lost preferred provenance: %+v", policy)
	}
}
