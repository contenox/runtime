package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

func init() {
	specs["inference.modeld.admission"] = Spec{Key: "inference.modeld.admission", Label: "Modeld admission policy", Meaning: "Shared queue bounds, execution cap, fairness and waiting limit as JSON.", Changes: "Changes admission across runtime instances sharing the database.", ReadBy: "Model runtime and ACP worker settings.", Unset: "Shipped interactive admission policy."}
	specs[DeviceCeilings] = Spec{Key: DeviceCeilings, Label: "Modeld device ceilings", Meaning: "Per memory pool, the share of the pool's total bytes local inference may hold, as a JSON object of pool id to a share greater than 0 and at most 1.", Changes: "Bounds every local role on a pool together: the planner's fit check and the model runtime's pool admission both refuse an allocation above the ceiling.", ReadBy: "Model runtime pool admission and the role recommendation planner.", Unset: "No ceiling: a pool's limit is its total less the reserved free memory."}
	for _, role := range []string{"primary", "alternative", "autocomplete", "next-edit", "embedding"} {
		key := "inference.modeld." + role
		specs[key] = Spec{Key: key, Label: "Modeld " + role + " worker", Meaning: "Native backend, percentage/auto memory budgets and idle retention as JSON.", Changes: "Configures the role's next local worker.", ReadBy: "Model runtime and ACP worker settings.", Unset: "Model-native backend and automatic resource budgets."}
	}
}

// DeviceCeilings is the per-pool memory ceiling setting: a JSON object mapping a
// memory pool id ("host" or an accelerator id the inventory reports) to the
// share of that pool's total bytes the model runtime may hold, 0 < share <= 1.
const DeviceCeilings = "inference.modeld.ceilings"

// ValidateDeviceCeiling refuses a pool id outside the inventory's spelling and a
// share outside (0, 1].
func ValidateDeviceCeiling(poolID string, share float64) error {
	if !validPoolID(poolID) {
		return fmt.Errorf("device ceiling pool id %q is not a memory pool id: use \"host\" or an accelerator id from the inventory", poolID)
	}
	if math.IsNaN(share) || share <= 0 || share > 1 {
		return fmt.Errorf("device ceiling for pool %s must be a share greater than 0 and at most 1, got %v", poolID, share)
	}
	return nil
}

// ParseDeviceCeilings decodes and validates a stored DeviceCeilings value. An
// empty value is the unset setting: no pool carries a ceiling.
func ParseDeviceCeilings(raw string) (map[string]float64, error) {
	ceilings := map[string]float64{}
	if strings.TrimSpace(raw) == "" {
		return ceilings, nil
	}
	if err := json.Unmarshal([]byte(raw), &ceilings); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object of pool id to share: %w", DeviceCeilings, err)
	}
	for poolID, share := range ceilings {
		if err := ValidateDeviceCeiling(poolID, share); err != nil {
			return nil, err
		}
	}
	return ceilings, nil
}

func validPoolID(poolID string) bool {
	if poolID == "" || len(poolID) > 128 {
		return false
	}
	for index, r := range poolID {
		alphanumeric := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alphanumeric && (index == 0 || !strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}
