package capacity_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
)

func TestUnit_ParseMemorySetting(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		bytes    int64
		fraction float64
		percent  bool
		auto     bool
	}{
		{raw: "1.5GiB", bytes: 3 << 29},
		{raw: "2GB", bytes: 2_000_000_000},
		{raw: "1024", bytes: 1024},
		{raw: "0B"},
		{raw: "  AuTo ", auto: true},
		{raw: " 12.5 % ", fraction: 0.125, percent: true},
		{raw: "100%", fraction: 1, percent: true},
		{raw: "0%", fraction: 0, percent: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := capacity.ParseMemorySetting(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Bytes != tc.bytes || got.Auto != tc.auto || (got.Fraction != nil) != tc.percent {
				t.Fatalf("ParseMemorySetting(%q) = %+v", tc.raw, got)
			}
			if tc.percent && *got.Fraction != tc.fraction {
				t.Fatalf("fraction = %v, want %v", *got.Fraction, tc.fraction)
			}
		})
	}
	for _, raw := range []string{"", "-1%", "101%", "NaN%", "+Inf%", "1%%", "-1B", "NaN", "InfGiB", "1e100GiB", "8GBB"} {
		t.Run(raw, func(t *testing.T) {
			if got, err := capacity.ParseMemorySetting(raw); err == nil {
				t.Fatalf("ParseMemorySetting(%q) = %+v, want error", raw, got)
			}
		})
	}
}

func TestUnit_ComputeReserveAndHeadroom(t *testing.T) {
	p, err := (capacity.Policy{}).WithHeadroom("0%")
	if err != nil {
		t.Fatal(err)
	}
	resolved := capacity.Resolve(capacity.Params{FreeBytes: 1000, HeadroomFrac: p.HeadroomFrac, HeadroomExplicit: p.HeadroomExplicit})
	if resolved.UsableBytes != 1000 {
		t.Fatalf("zero headroom reduced budget: %d", resolved.UsableBytes)
	}
	for _, tc := range []struct {
		setting string
		want    int64
	}{{"auto", 768}, {"25%", 250}, {"0%", 0}, {"512", 512}} {
		got, err := capacity.ResolveComputeReserve(tc.setting, 768, capacity.DeviceSnapshot{TotalBytes: 2000, FreeBytes: 1000})
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %d, %v; want %d", tc.setting, got, err, tc.want)
		}
	}
	if _, err := capacity.ResolveComputeReserve("25%", 768, capacity.DeviceSnapshot{}); err == nil {
		t.Fatal("missing device snapshot accepted")
	}
	for _, tc := range []struct {
		setting string
		want    float64
	}{{"auto", capacity.DefaultHeadroomFrac}, {"0%", 0}, {"25%", 0.25}} {
		got, err := (capacity.Policy{}).WithHeadroom(tc.setting)
		if err != nil || got.HeadroomFrac != tc.want {
			t.Fatalf("%s: got %v, %v", tc.setting, got.HeadroomFrac, err)
		}
	}
	if _, err := (capacity.Policy{}).WithHeadroom("512MiB"); err == nil {
		t.Fatal("byte headroom accepted")
	}
}

func TestUnit_Policy_PercentagesUseTheirMemoryPools(t *testing.T) {
	p, err := (capacity.Policy{}).WithMemorySettings("50%", "10%", "25%")
	if err != nil {
		t.Fatal(err)
	}
	dev := capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 1000, FreeBytes: 800}
	host := capacity.DeviceSnapshot{Kind: "system", TotalBytes: 8000, FreeBytes: 4000}
	p = capacity.WithHostColdDefaults(capacity.WithResidentDefault(p, dev), host)
	if p.MaxResidentBytes != 400 || p.MinFreeBytes != 100 || p.HostColdBudgetBytes != 1000 {
		t.Fatalf("resolved policy = %+v, want resident 400/reserve 100/cold 1000", p)
	}
	dev.FreeBytes, dev.TotalBytes, host.FreeBytes = 400, 2000, 2000
	p = capacity.WithHostColdDefaults(capacity.WithResidentDefault(p, dev), host)
	if p.MaxResidentBytes != 200 || p.MinFreeBytes != 200 || p.HostColdBudgetBytes != 500 {
		t.Fatalf("fresh resolution = %+v, want resident 200/reserve 200/cold 500", p)
	}
}

func TestUnit_Policy_ZeroPercentageDisablesReserveAndCold(t *testing.T) {
	p, err := (capacity.Policy{}).WithMemorySettings("auto", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	p = capacity.WithHostColdDefaults(capacity.WithResidentDefault(p, capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 8 << 30, FreeBytes: 4 << 30}), capacity.DeviceSnapshot{FreeBytes: 8 << 30})
	if p.MinFreeBytes != 0 || p.HostColdBudgetBytes != 0 {
		t.Fatalf("disabled reserve/cold = %d/%d, want 0/0", p.MinFreeBytes, p.HostColdBudgetBytes)
	}
	if _, err := p.WithMemorySettings("0%", "", ""); err == nil {
		t.Fatal("resident 0% must fail")
	}
}

func TestUnit_ResolvePolicy_RejectsUnavailablePercentageBases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resident string
		reserve  string
		cold     string
		device   capacity.DeviceSnapshot
		host     capacity.DeviceSnapshot
		flag     string
	}{
		{name: "resident", resident: "50%", reserve: "0%", cold: "0%", flag: "--mem-max"},
		{name: "reserve", resident: "1GiB", reserve: "10%", cold: "0%", device: capacity.DeviceSnapshot{FreeBytes: 1000}, flag: "--mem-reserve"},
		{name: "host cold", resident: "1GiB", reserve: "0%", cold: "25%", flag: "--mem-cold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := (capacity.Policy{}).WithMemorySettings(tc.resident, tc.reserve, tc.cold)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := capacity.ResolvePolicy(p, tc.device, tc.host); err == nil || !strings.Contains(err.Error(), tc.flag) {
				t.Fatalf("ResolvePolicy error = %v, want actionable %s error", err, tc.flag)
			}
		})
	}
}

func TestUnit_ResolvePolicy_AllowsMeasuredZeroAndDisabledBudgets(t *testing.T) {
	p, err := (capacity.Policy{}).WithMemorySettings("50%", "0%", "25%")
	if err != nil {
		t.Fatal(err)
	}
	p, err = capacity.ResolvePolicy(p, capacity.DeviceSnapshot{TotalBytes: 1000}, capacity.DeviceSnapshot{TotalBytes: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxResidentBytes != 0 || p.MinFreeBytes != 0 || p.HostColdBudgetBytes != 0 {
		t.Fatalf("zero free pools resolved nonzero policy: %+v", p)
	}
	p, err = p.WithMemorySettings("1GiB", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.ResolvePolicy(p, capacity.DeviceSnapshot{}, capacity.DeviceSnapshot{}); err != nil {
		t.Fatalf("disabled percentage budgets require no snapshot: %v", err)
	}
}

func TestUnit_ResolvePolicy_ValidatesProgrammaticFractions(t *testing.T) {
	for _, fraction := range []float64{-0.1, 0, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := capacity.ResolvePolicy(capacity.Policy{MaxResidentFrac: &fraction}, capacity.DeviceSnapshot{FreeBytes: 1000}, capacity.DeviceSnapshot{FreeBytes: 1000}); err == nil {
			t.Fatalf("resident fraction %v should fail", fraction)
		}
	}
}

func TestUnit_Policy_AutoRestoresDefaultsAndBytesReplaceFractions(t *testing.T) {
	p, err := capacity.DefaultPolicy().WithMemorySettings("2GiB", "1GiB", "3GiB")
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxResidentFrac != nil || p.MinFreeFrac != nil || p.HostColdBudgetFrac != nil {
		t.Fatalf("byte settings retained fractions: %+v", p)
	}
	p = capacity.WithHostColdDefaults(capacity.WithResidentDefault(p, capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 16 << 30, FreeBytes: 12 << 30}), capacity.DeviceSnapshot{FreeBytes: 20 << 30})
	if p.MaxResidentBytes != 2<<30 || p.MinFreeBytes != 1<<30 || p.HostColdBudgetBytes != 3<<30 {
		t.Fatalf("explicit bytes changed: %+v", p)
	}
	p, err = p.WithMemorySettings("auto", "auto", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxResidentBytes != 0 || p.MinFreeBytes != 0 || p.HostColdBudgetBytes != 0 {
		t.Fatalf("auto retained byte ceilings: %+v", p)
	}
	if p.MaxResidentFrac == nil || *p.MaxResidentFrac != capacity.DefaultMaxResidentFrac || p.HostColdBudgetFrac == nil || *p.HostColdBudgetFrac != capacity.DefaultHostColdFrac || p.MinFreeFrac != nil {
		t.Fatalf("auto fractions = %+v", p)
	}
	p = capacity.WithResidentDefault(p, capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 6 << 30, FreeBytes: 5 << 30})
	if p.MinFreeBytes != capacity.DefaultAcceleratorMinFreeBytes {
		t.Fatalf("auto reserve = %d, want floor %d", p.MinFreeBytes, capacity.DefaultAcceleratorMinFreeBytes)
	}
}

func TestUnit_LoadPolicy_PercentageAndAutoPrecedence(t *testing.T) {
	clearMemoryPolicyEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "modeld.json"), []byte(`{"memory":{"max_resident_bytes":1024,"max_resident":"80%","reserve_free":"0%","host_cold_budget":"50%"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTENOX_MODELD_MEM_MAX", "auto")
	t.Setenv("CONTENOX_MODELD_MEM_COLD", "2GiB")
	p, warnings := capacity.LoadPolicy(dir)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if p.MaxResidentBytes != 0 || p.MaxResidentFrac == nil || *p.MaxResidentFrac != capacity.DefaultMaxResidentFrac || p.MinFreeFrac == nil || *p.MinFreeFrac != 0 || p.HostColdBudgetFrac != nil || p.HostColdBudgetBytes != 2<<30 {
		t.Fatalf("merged policy = %+v", p)
	}
	t.Setenv("CONTENOX_MODELD_MEM_MAX", "101%")
	p, warnings = capacity.LoadPolicy(dir)
	if len(warnings) != 1 || p.MaxResidentFrac == nil || *p.MaxResidentFrac != 0.8 {
		t.Fatalf("invalid override lost config or warning: policy=%+v warnings=%v", p, warnings)
	}
}

func TestUnit_LoadPolicy_NumericBytesAndFractions(t *testing.T) {
	clearMemoryPolicyEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "modeld.json"), []byte(`{"memory":{"max_resident_bytes":1024,"min_free_frac":0,"host_cold_budget_frac":0.4}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, warnings := capacity.LoadPolicy(dir)
	if len(warnings) != 0 || p.MaxResidentBytes != 1024 || p.MaxResidentFrac != nil || p.MinFreeFrac == nil || *p.MinFreeFrac != 0 || p.HostColdBudgetFrac == nil || *p.HostColdBudgetFrac != 0.4 {
		t.Fatalf("policy=%+v warnings=%v", p, warnings)
	}
}

func TestUnit_LoadPolicy_MalformedConfigDoesNotApplyPartialValues(t *testing.T) {
	clearMemoryPolicyEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "modeld.json"), []byte(`{"memory":{"max_resident_bytes":1024,"min_free_bytes":"bad"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, warnings := capacity.LoadPolicy(dir)
	if len(warnings) != 1 || p.MaxResidentBytes != 0 || p.MaxResidentFrac == nil || *p.MaxResidentFrac != capacity.DefaultMaxResidentFrac {
		t.Fatalf("policy=%+v warnings=%v", p, warnings)
	}
}

func clearMemoryPolicyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CONTENOX_MODELD_MEM_MAX", "CONTENOX_MODELD_MEM_RESERVE", "CONTENOX_MODELD_MEM_COLD", "CONTENOX_MODELD_MEM_HEADROOM", "CONTENOX_MODELD_MIN_HOT_CONTEXT"} {
		t.Setenv(name, "")
	}
}
