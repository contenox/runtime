package libsandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_OverlayEnv_AddsAndOverrides(t *testing.T) {
	env := []string{"PATH=/usr/bin", "FOO=old"}
	got := OverlayEnv(env, map[string]string{"FOO": "new", "BAR": "baz"})
	require.Equal(t, []string{"BAR=baz", "FOO=new", "PATH=/usr/bin"}, got)
}

func TestUnit_OverlayEnv_EmptySetIsNoop(t *testing.T) {
	env := []string{"PATH=/usr/bin", "FOO=old"}
	require.Equal(t, env, OverlayEnv(env, nil))
	require.Equal(t, env, OverlayEnv(env, map[string]string{}))
}

func TestUnit_OverlayEnv_KeepsEmptyValueDropsMalformed(t *testing.T) {
	got := OverlayEnv([]string{"NOEQ", "A=1"}, map[string]string{"EMPTY": ""})
	require.Equal(t, []string{"A=1", "EMPTY="}, got)
}

func TestUnit_OverlayEnv_ComposesAfterScrub(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "AWS_SECRET_ACCESS_KEY=shhh"}
	scrubbed := EnvScrub(ScrubDenySecrets, nil, nil)(parent)
	got := OverlayEnv(scrubbed, map[string]string{"AWS_SECRET_ACCESS_KEY": "operator-set", "APP_ENV": "prod"})
	require.Equal(t, []string{"APP_ENV=prod", "AWS_SECRET_ACCESS_KEY=operator-set", "PATH=/usr/bin"}, got)
}
