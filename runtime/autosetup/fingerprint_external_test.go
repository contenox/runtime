package autosetup_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/modelhardware"
)

func TestUnit_Fingerprint_NilAndEmptyCollectionsMatch(t *testing.T) {
	answered := autosetup.Plan{
		Mode: "local", Backend: "llama", Assignments: []autosetup.PlannedRole{}, Hardware: modelhardware.Hardware{},
		Roles: []autosetup.RolePlan{{
			Constraint:   autosetup.RoleConstraint{Role: "embedding", Policy: "local", AllowedConnections: []string{}},
			State:        "acquire",
			Alternatives: []autosetup.RoleCandidate{},
			Repairs:      []string{},
		}},
	}
	echoed := autosetup.Plan{
		Mode: "local", Backend: "llama", Assignments: nil, Hardware: modelhardware.Hardware{Devices: []modelhardware.Device{}},
		Roles: []autosetup.RolePlan{{
			Constraint:   autosetup.RoleConstraint{Role: "embedding", Policy: "local", AllowedConnections: nil},
			State:        "acquire",
			Alternatives: nil,
			Repairs:      nil,
		}},
	}
	first, err := autosetup.Fingerprint(answered)
	if err != nil {
		t.Fatal(err)
	}
	second, err := autosetup.Fingerprint(echoed)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("a plan echoed with nil collections fingerprints %s, the answered plan %s", second, first)
	}
	changed := echoed
	changed.Backend = "openvino"
	third, err := autosetup.Fingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("a changed plan must not keep the answered fingerprint")
	}
	stamped := answered
	stamped.Fingerprint = first
	fourth, err := autosetup.Fingerprint(stamped)
	if err != nil {
		t.Fatal(err)
	}
	if fourth != first {
		t.Fatal("the carried fingerprint must not feed its own computation")
	}
}
