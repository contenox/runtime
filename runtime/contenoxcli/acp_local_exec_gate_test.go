package contenoxcli

import (
	"testing"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/stretchr/testify/assert"
)

func TestUnit_ACPLocalExecGate_AutoModeRequiresAnAllowlist(t *testing.T) {
	assert.Equal(t, localtools.GateRequireAllowlist, acpLocalExecGate(false))
}

func TestUnit_ACPLocalExecGate_HITLHandsTheDecisionToThePolicy(t *testing.T) {
	assert.Equal(t, localtools.GatePolicy, acpLocalExecGate(true))
}
