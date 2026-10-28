package localtools

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_WebTools_BlockedEgressPredicate(t *testing.T) {
	for _, s := range []string{"169.254.169.254", "169.254.0.1", "fe80::1"} {
		require.Truef(t, isBlockedEgressIP(net.ParseIP(s)), "%s is link-local / cloud-metadata and must be blocked", s)
	}
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "8.8.8.8", "::1"} {
		require.Falsef(t, isBlockedEgressIP(net.ParseIP(s)), "%s is not link-local; on-host and public services must still resolve", s)
	}
	require.False(t, isBlockedEgressIP(nil))
}
