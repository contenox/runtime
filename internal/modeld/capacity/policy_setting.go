package capacity

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MemorySetting is an allocation expressed as bytes, a fraction, or automatic
// policy. A non-nil Fraction distinguishes an explicit zero from automatic.
type MemorySetting struct {
	Bytes    int64
	Fraction *float64
	Auto     bool
}

// WithHeadroom applies automatic or percentage allocation headroom.
func (p Policy) WithHeadroom(raw string) (Policy, error) {
	if raw == "" {
		return p, nil
	}
	setting, err := ParseMemorySetting(raw)
	if err != nil {
		return Policy{}, err
	}
	if setting.Auto {
		p.HeadroomFrac = DefaultHeadroomFrac
		p.HeadroomExplicit = false
	} else if setting.Fraction != nil {
		p.HeadroomFrac = *setting.Fraction
		p.HeadroomExplicit = true
	} else {
		return Policy{}, fmt.Errorf("headroom requires auto or a percentage")
	}
	return p, nil
}

// ResolveComputeReserve resolves an explicit compute target against available device memory.
func ResolveComputeReserve(raw string, automatic int64, device DeviceSnapshot) (int64, error) {
	setting, err := ParseMemorySetting(raw)
	if err != nil {
		return 0, err
	}
	if setting.Auto {
		return automatic, nil
	}
	if setting.Fraction != nil {
		if *setting.Fraction == 0 {
			return 0, nil
		}
		if device.FreeBytes < 0 || device.FreeBytes == 0 && device.TotalBytes <= 0 {
			return 0, fmt.Errorf("compute reserve percentage requires available device memory")
		}
		return int64(float64(device.FreeBytes) * *setting.Fraction), nil
	}
	return setting.Bytes, nil
}

// ParseMemorySetting parses byte sizes, percentages in [0,100], or "auto".
func ParseMemorySetting(raw string) (MemorySetting, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "auto") {
		return MemorySetting{Auto: true}, nil
	}
	if raw == "" {
		return MemorySetting{}, fmt.Errorf("memory allocation is empty; use a byte size, percentage, or auto")
	}
	if strings.HasSuffix(raw, "%") {
		percentage, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(raw, "%")), 64)
		if err != nil || math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0 || percentage > 100 {
			return MemorySetting{}, fmt.Errorf("invalid memory percentage %q; expected a percentage in [0,100]", raw)
		}
		fraction := percentage / 100
		return MemorySetting{Fraction: &fraction}, nil
	}
	bytes, err := ParseBytes(raw)
	if err != nil {
		return MemorySetting{}, err
	}
	return MemorySetting{Bytes: bytes}, nil
}

// DefaultPolicy returns the automatic resident and host cold fractions and
// headroom. Its nil reserve fraction selects the accelerator reserve floor.
func DefaultPolicy() Policy {
	resident, cold := DefaultMaxResidentFrac, DefaultHostColdFrac
	return Policy{
		MaxResidentFrac:    &resident,
		HostColdBudgetFrac: &cold,
		HeadroomFrac:       DefaultHeadroomFrac,
	}
}

// ResolvePolicy resolves allocation fractions from device and host snapshots.
// Missing memory measurements required by positive fractions return an error.
// A measured zero free pool is valid when the snapshot supplies a total.
func ResolvePolicy(p Policy, device, host DeviceSnapshot) (Policy, error) {
	for _, setting := range []struct {
		name      string
		flag      string
		bytes     int64
		fraction  *float64
		allowZero bool
		snapshot  DeviceSnapshot
		total     bool
	}{
		{"resident", "--mem-max", p.MaxResidentBytes, p.MaxResidentFrac, false, device, false},
		{"reserve", "--mem-reserve", p.MinFreeBytes, p.MinFreeFrac, true, device, true},
		{"host cold", "--mem-cold", p.HostColdBudgetBytes, p.HostColdBudgetFrac, true, host, false},
	} {
		if setting.bytes < 0 {
			return Policy{}, fmt.Errorf("%s allocation bytes must be non-negative; set %s", setting.name, setting.flag)
		}
		if setting.fraction == nil {
			continue
		}
		fraction := *setting.fraction
		if math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction < 0 || fraction > 1 || fraction == 0 && !setting.allowZero {
			return Policy{}, fmt.Errorf("%s allocation fraction is invalid; set %s to a valid byte size, percentage, or auto", setting.name, setting.flag)
		}
		if fraction == 0 {
			continue
		}
		if setting.total && setting.snapshot.TotalBytes <= 0 {
			return Policy{}, fmt.Errorf("%s allocation percentage requires total device memory; set %s to a byte size or auto", setting.name, setting.flag)
		}
		if !setting.total && (setting.snapshot.FreeBytes < 0 || setting.snapshot.FreeBytes == 0 && setting.snapshot.TotalBytes <= 0) {
			return Policy{}, fmt.Errorf("%s allocation percentage requires available memory; provide a memory snapshot or set %s to a byte size", setting.name, setting.flag)
		}
	}
	return WithHostColdDefaults(WithResidentDefault(p, device), host), nil
}

// WithMemorySettings applies resident, reserve, and host cold overrides. Empty
// settings preserve the corresponding policy; "auto" restores its defaults.
// Zero percentages disable reserve or host cold storage; resident must be
// positive. Byte sizes preserve the legacy zero-means-automatic behavior.
func (p Policy) WithMemorySettings(resident, reserve, cold string) (Policy, error) {
	defaults := DefaultPolicy()
	settings := []struct {
		name         string
		raw          string
		bytes        *int64
		fraction     **float64
		autoFraction *float64
		allowZero    bool
	}{
		{"resident", resident, &p.MaxResidentBytes, &p.MaxResidentFrac, defaults.MaxResidentFrac, false},
		{"reserve", reserve, &p.MinFreeBytes, &p.MinFreeFrac, nil, true},
		{"host cold", cold, &p.HostColdBudgetBytes, &p.HostColdBudgetFrac, defaults.HostColdBudgetFrac, true},
	}
	for _, setting := range settings {
		if strings.TrimSpace(setting.raw) == "" {
			continue
		}
		parsed, err := ParseMemorySetting(setting.raw)
		if err != nil {
			return Policy{}, fmt.Errorf("%s allocation: %w", setting.name, err)
		}
		if parsed.Fraction != nil && *parsed.Fraction == 0 && !setting.allowZero {
			return Policy{}, fmt.Errorf("resident allocation must be greater than 0%%")
		}
		*setting.bytes = parsed.Bytes
		*setting.fraction = parsed.Fraction
		if setting.name == "resident" {
			p.MaxResidentPreferred = parsed.Auto || parsed.Fraction != nil
		}
		if parsed.Auto || parsed.Bytes == 0 && parsed.Fraction == nil {
			*setting.fraction = setting.autoFraction
		}
	}
	return p, nil
}

func applyFractionSetting(warnings *[]string, name string, raw *float64, bytes *int64, fraction **float64, allowZero bool) {
	if raw == nil {
		return
	}
	if math.IsNaN(*raw) || math.IsInf(*raw, 0) || *raw < 0 || *raw > 1 || *raw == 0 && !allowZero {
		*warnings = append(*warnings, fmt.Sprintf("memory setting ignored: invalid allocation fraction (setting=%s value=%v)", name, *raw))
		return
	}
	*bytes = 0
	*fraction = raw
}

type memoryPolicyConfig struct {
	MaxResidentBytes    int64    `json:"max_resident_bytes"`
	MinFreeBytes        int64    `json:"min_free_bytes"`
	HostColdBudgetBytes int64    `json:"host_cold_budget_bytes"`
	MaxResidentFrac     *float64 `json:"max_resident_frac"`
	MinFreeFrac         *float64 `json:"min_free_frac"`
	HostColdBudgetFrac  *float64 `json:"host_cold_budget_frac"`
	MinHotContextTokens *int     `json:"min_hot_context_tokens"`
	MaxResident         string   `json:"max_resident"`
	ReserveFree         string   `json:"reserve_free"`
	HostColdBudget      string   `json:"host_cold_budget"`
	HeadroomFrac        float64  `json:"headroom_frac"`
}
