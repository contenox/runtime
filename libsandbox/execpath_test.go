package libsandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_canonicalPATH_IsSystemExecDirsJoined(t *testing.T) {
	require.Equal(t, "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", canonicalPATH())
	for _, dir := range SystemExecDirs() {
		require.True(t, strings.HasPrefix(dir, "/"), "exec dir %q must be absolute", dir)
	}
}

func TestUnit_validatePATH_CanonicalIsAlwaysValid(t *testing.T) {
	require.NoError(t, validatePATH(canonicalPATH(), "/h", nil))
}

func TestUnit_confinedPATH_FiltersToExecSurface(t *testing.T) {
	fs := []FSCarveout{{Path: "/opt/toolchain", Mode: ModeRO, Needs: "node"}}
	op := "/opt/toolchain/node/bin:/usr/bin:/home/dev/.cargo/bin:/sbin"

	got := confinedPATH(op, "/h", fs)

	require.Equal(t, "/opt/toolchain/node/bin:/usr/bin:/sbin", got)
	require.NoError(t, validatePATH(got, "/h", fs))
}

func TestUnit_confinedPATH_KeepsCarvedTildeToolchain(t *testing.T) {
	fs := []FSCarveout{{Path: "~/.nvm", Mode: ModeRO, Needs: "node runtime"}}
	op := "/home/naro/.nvm/versions/node/v24.18.0/bin:/usr/bin:/home/naro/.local/bin"

	got := confinedPATH(op, "/home/naro", fs)

	require.Equal(t, "/home/naro/.nvm/versions/node/v24.18.0/bin:/usr/bin", got)
}

func TestUnit_confinedPATH_FallsBackToCanonicalWhenEmpty(t *testing.T) {
	require.Equal(t, canonicalPATH(), confinedPATH("", "/h", nil))
	require.Equal(t, canonicalPATH(), confinedPATH("/opt/nowhere:relative/bin", "/h", nil))
}

func TestUnit_validatePATH_EmptyIsInert(t *testing.T) {
	require.NoError(t, validatePATH("", "/h", nil))
	require.NoError(t, validatePATH("   ", "/h", nil))
}

func TestUnit_validatePATH_RejectsUncoveredDir(t *testing.T) {
	err := validatePATH("/usr/bin:/opt/rogue/bin", "/h", nil)
	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "/opt/rogue/bin")
}

func TestUnit_validatePATH_RejectsRelativeAndEmptyEntry(t *testing.T) {
	require.ErrorIs(t, validatePATH("/usr/bin:rel/bin", "/h", nil), ErrInvalidSpec)
	require.ErrorIs(t, validatePATH("/usr/bin::/bin", "/h", nil), ErrInvalidSpec)
}

func TestUnit_validatePATH_AllowsCarveoutCoveredDir(t *testing.T) {
	fs := []FSCarveout{{Path: "/opt/tools", Mode: ModeRO, Needs: "toolchain"}}
	require.NoError(t, validatePATH("/opt/tools", "/h", fs))
	require.NoError(t, validatePATH("/opt/tools/bin", "/h", fs))
	require.NoError(t, validatePATH("/usr/bin:/opt/tools/bin", "/h", fs))
}

func TestUnit_validatePATH_ResolvesTildeCarveoutAgainstHome(t *testing.T) {
	fs := []FSCarveout{{Path: "~/.local/bin", Mode: ModeRO, Needs: "user bin"}}
	require.NoError(t, validatePATH("/scoped/home/.local/bin", "/scoped/home", fs))
	require.ErrorIs(t, validatePATH("/scoped/home/.local/bin", "/scoped/home", nil), ErrInvalidSpec)
}

func TestUnit_pathWithin_RespectsPathBoundaries(t *testing.T) {
	roots := []string{"/usr"}
	require.True(t, pathWithin("/usr", roots))
	require.True(t, pathWithin("/usr/bin", roots))
	require.False(t, pathWithin("/usrlocal", roots))
	require.False(t, pathWithin("/", roots))
}

func TestUnit_lookupEnv(t *testing.T) {
	env := []string{"HOME=/h", "PATH=/a", "PATH=/b"}
	require.Equal(t, "/b", lookupEnv(env, "PATH"))
	require.Equal(t, "/h", lookupEnv(env, "HOME"))
	require.Equal(t, "", lookupEnv(env, "MISSING"))
}
