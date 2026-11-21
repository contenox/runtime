package inferencesetup

import (
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestVerifiedMediaDigestUsesMatchingExecution(t *testing.T) {
	report := transport.SessionExecution{Backend: "openvino", ModelName: "artist", Digest: "verified"}
	digest, err := verifiedMediaDigest("artist", []transport.SessionExecution{{Backend: "openvino", ModelName: "other", Digest: "other"}, report})
	require.NoError(t, err)
	require.Equal(t, "verified", digest)
	changed := report
	changed.Digest = "other"
	for _, reports := range [][]transport.SessionExecution{nil, {changed, report}, {{Backend: "openvino", ModelName: "artist"}}, {{Backend: "llama", ModelName: "artist", Digest: "other"}}} {
		_, err := verifiedMediaDigest("artist", reports)
		require.ErrorIs(t, err, transport.ErrDigestMismatch)
	}
}
