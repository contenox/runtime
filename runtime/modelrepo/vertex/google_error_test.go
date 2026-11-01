package vertex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_GoogleErrorMessage_ShowsTheReasonNotTheJSON(t *testing.T) {
	body := []byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"This API method requires billing to be enabled."}}`)
	require.Equal(t, "PERMISSION_DENIED: This API method requires billing to be enabled.", googleErrorMessage(body))
}

func TestUnit_GoogleErrorMessage_FallsBackToTheRawBody(t *testing.T) {
	require.Equal(t, "upstream exploded", googleErrorMessage([]byte("  upstream exploded \n")))
}
