package settings

import "sort"

// Spec describes a setting independently of its storage and presentation.
type Spec struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Meaning string `json:"meaning"`
	Changes string `json:"effect"`
	ReadBy  string `json:"read_by"`
	Env     string `json:"environment_variable"`
	Unset   string `json:"unset_behavior"`
}

var specs = map[string]Spec{}

// Lookup reports the setting definition for a Modeld configuration key.
func Lookup(key string) (Spec, bool) { spec, ok := specs[key]; return spec, ok }

// All returns the setting definitions sorted by public name.
func All() []Spec {
	out := make([]Spec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
