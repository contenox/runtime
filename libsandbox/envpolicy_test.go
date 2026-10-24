package libsandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_DefaultEnvPolicy_ResolveKeepsSafeBaseDropsSecrets(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"USER=agent",
		"HOME=/home/real",
		"AWS_SECRET_ACCESS_KEY=shhh",
		"ANTHROPIC_API_KEY=sk-ant-xxx",
		"CONTENOX_TOKEN=leak",
		"GITHUB_TOKEN=ghp_xxx",
	}

	got := DefaultEnvPolicy().Resolve(parent)

	require.Equal(t, []string{"LANG", "LC_ALL", "PATH", "USER"}, got)
}

func TestUnit_EnvPolicy_ResolveOnlyReturnsNamesPresentInParent(t *testing.T) {
	got := DefaultEnvPolicy().Resolve([]string{"PATH=/usr/bin"})
	require.Equal(t, []string{"PATH"}, got)
}

func TestUnit_EnvPolicy_DenyWinsOverAllow(t *testing.T) {
	parent := []string{
		"AWS_REGION=eu-west-1",
		"AWS_SECRET_ACCESS_KEY=shhh",
		"AWS_SESSION_TOKEN=tok",
	}

	got := DefaultEnvPolicy().Allowing("AWS_*").Resolve(parent)

	require.Equal(t, []string{"AWS_REGION"}, got)
}

func TestUnit_EnvPolicy_GlobPrefixAndSuffix(t *testing.T) {
	parent := []string{
		"LC_ALL=C",
		"LC_CTYPE=UTF-8",
		"HTTP_PROXY=http://p",
		"HTTPS_PROXY=http://p",
		"NO_PROXY=localhost",
		"PATH=/usr/bin",
	}

	got := EnvPolicy{Allow: []string{"LC_*", "*_PROXY"}}.Resolve(parent)

	require.Equal(t, []string{"HTTPS_PROXY", "HTTP_PROXY", "LC_ALL", "LC_CTYPE", "NO_PROXY"}, got)
}

func TestUnit_EnvPolicy_ResolveSortedAndDeduped(t *testing.T) {
	parent := []string{"TERM=xterm", "PATH=/a", "PATH=/b"}

	got := DefaultEnvPolicy().Resolve(parent)

	require.Equal(t, []string{"PATH", "TERM"}, got)
}

func TestUnit_EnvPolicy_ResolveSkipsMalformedParentEntry(t *testing.T) {
	got := DefaultEnvPolicy().Resolve([]string{"NOEQUALS", "PATH=/usr/bin"})
	require.Equal(t, []string{"PATH"}, got)
}

func TestUnit_EnvPolicy_BareStarAllowsAllButDenyStillWins(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"RANDOM_VAR=ok",
		"CONTENOX_DB=/x",
		"SOME_API_KEY=secret",
	}

	got := DefaultEnvPolicy().Allowing("*").Resolve(parent)

	require.Equal(t, []string{"PATH", "RANDOM_VAR"}, got)
}

func TestUnit_EnvPolicy_AllowingDenyingReturnCopies(t *testing.T) {
	base := DefaultEnvPolicy()
	baseAllowLen := len(base.Allow)
	baseDenyLen := len(base.Deny)

	extended := base.Allowing("HTTP_PROXY").Denying("DANGER_*")

	require.Len(t, base.Allow, baseAllowLen, "base Allow must be unchanged")
	require.Len(t, base.Deny, baseDenyLen, "base Deny must be unchanged")
	require.Contains(t, extended.Allow, "HTTP_PROXY")
	require.Contains(t, extended.Deny, "DANGER_*")
	require.NotContains(t, base.Allow, "HTTP_PROXY")

	require.NotContains(t, DefaultEnvPolicy().Allow, "HTTP_PROXY")
}

func TestUnit_DefaultEnvAllow_ExcludesHome(t *testing.T) {
	require.NotContains(t, DefaultEnvAllow(), "HOME")
}

func TestUnit_DefaultEnvDeny_VetoesControlPlane(t *testing.T) {
	require.True(t, matchesAny(DefaultEnvDeny(), "CONTENOX_DB"))
	require.True(t, matchesAny(DefaultEnvDeny(), "CONTENOX_TOKEN"))
}

func TestUnit_envNameMatches(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"PATH", "PATH", true},
		{"PATH", "PATHEXT", false},
		{"LC_*", "LC_ALL", true},
		{"LC_*", "LC_", true},
		{"LC_*", "LANG", false},
		{"*_TOKEN", "GITHUB_TOKEN", true},
		{"*_TOKEN", "TOKEN_SUFFIX", false},
		{"*", "ANYTHING", true},
		{"", "PATH", false},
		{"path", "PATH", false},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, envNameMatches(c.pattern, c.name),
			"envNameMatches(%q, %q)", c.pattern, c.name)
	}
}

func TestUnit_ParseEnvList(t *testing.T) {
	require.Equal(t, []string{"PATH", "LC_*", "HTTP_PROXY", "GOCACHE"},
		ParseEnvList("PATH, LC_*  HTTP_PROXY;GOCACHE"))
	require.Equal(t, []string{"A", "B"}, ParseEnvList("A\nB\t"))
	require.Nil(t, ParseEnvList(""))
	require.Nil(t, ParseEnvList("   ,; \n "))
}

func TestUnit_EnvPolicy_ResolveFeedsScrubEnv(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"LANG=C",
		"HOME=/home/real",
		"AWS_SECRET_ACCESS_KEY=shhh",
		"CONTENOX_TOKEN=leak",
	}

	allow := DefaultEnvPolicy().Resolve(parent)
	got := scrubEnv(parent, allow, nil, "/scoped/home")

	require.Equal(t, []string{
		"HOME=/scoped/home",
		"LANG=C",
		"PATH=" + canonicalPATH(),
	}, got)
}

func TestUnit_EnvPolicy_ApplyReturnsPairsAndDoesNotForceHome(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/real",
		"GITHUB_TOKEN=ghp_xxx",
		"CONTENOX_DB=/x",
	}

	got := EnvPolicy{Allow: []string{"*"}, Deny: DefaultEnvDeny()}.Apply(parent)

	require.Equal(t, []string{"HOME=/home/real", "PATH=/usr/bin"}, got)
}

func TestUnit_EnvDeny_ControlPlaneAndSecretSplit(t *testing.T) {
	require.Equal(t, []string{"CONTENOX_*"}, ControlPlaneEnvDeny())
	require.Contains(t, SecretEnvDeny(), "*_TOKEN")
	require.NotContains(t, SecretEnvDeny(), "CONTENOX_*")
	require.Equal(t, append(ControlPlaneEnvDeny(), SecretEnvDeny()...), DefaultEnvDeny())
}

func TestUnit_EnvPolicy_StrictCanRepermitOneCredential(t *testing.T) {
	strict := EnvPolicy{Allow: DefaultEnvAllow(), Deny: ControlPlaneEnvDeny()}
	parent := []string{
		"PATH=/usr/bin",
		"NPM_TOKEN=npm_trusted",
		"AWS_SECRET_ACCESS_KEY=shhh",
		"CONTENOX_TOKEN=leak",
	}

	got := strict.Allowing("NPM_TOKEN").Apply(parent)

	require.Equal(t, []string{"NPM_TOKEN=npm_trusted", "PATH=/usr/bin"}, got)
}
