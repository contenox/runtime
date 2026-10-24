package modelrepo_test

import (
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func terminal(reason string, terminated bool) *modelrepo.StreamParcel {
	return &modelrepo.StreamParcel{Terminal: &modelrepo.StreamTerminal{
		FinishReason: reason,
		Terminated:   terminated,
	}}
}

func TestUnit_StreamAssembler_TerminatedStreamCompletes(t *testing.T) {
	asm := modelrepo.NewStreamAssembler("provider", "model")
	require.NoError(t, asm.Consume(&modelrepo.StreamParcel{Data: "half "}))
	require.NoError(t, asm.Consume(&modelrepo.StreamParcel{Data: "a turn"}))
	require.NoError(t, asm.Consume(terminal("content_filter", true)))

	res, err := asm.Result()
	require.NoError(t, err)
	assert.Equal(t, "half a turn", res.Content)
	assert.Equal(t, "content_filter", res.FinishReason)
}

func TestUnit_StreamAssembler_UnterminatedStreamIsTruncated(t *testing.T) {
	asm := modelrepo.NewStreamAssembler("provider", "model")
	require.NoError(t, asm.Consume(&modelrepo.StreamParcel{Data: "half a tu"}))
	require.NoError(t, asm.Consume(terminal("", false)))

	_, err := asm.Result()
	require.Error(t, err)
	assert.ErrorIs(t, err, modelrepo.ErrStreamTruncated)
	assert.Contains(t, err.Error(), "provider")
	assert.Contains(t, err.Error(), "model")
}

func TestUnit_StreamAssembler_VendorFinishReasonWithoutMarkerCompletes(t *testing.T) {
	for _, reason := range []string{"length", "content_filter", "max_tokens"} {
		asm := modelrepo.NewStreamAssembler("provider", "model")
		require.NoError(t, asm.Consume(&modelrepo.StreamParcel{Data: "partial"}))
		require.NoError(t, asm.Consume(terminal(reason, false)))

		res, err := asm.Result()
		require.NoError(t, err, "reason %s", reason)
		assert.Equal(t, reason, res.FinishReason)
		assert.Equal(t, "partial", res.Content)
	}
}

func TestUnit_StreamAssembler_MarkerWithoutFinishReasonCompletes(t *testing.T) {
	asm := modelrepo.NewStreamAssembler("provider", "model")
	require.NoError(t, asm.Consume(terminal("", true)))

	_, err := asm.Result()
	assert.NoError(t, err)
}

func TestUnit_StreamAssembler_MissingTerminalIsAContractViolation(t *testing.T) {
	asm := modelrepo.NewStreamAssembler("provider", "model")
	require.NoError(t, asm.Consume(&modelrepo.StreamParcel{Data: "text"}))

	_, err := asm.Result()
	require.Error(t, err)
	assert.NotErrorIs(t, err, modelrepo.ErrStreamTruncated)
	assert.Contains(t, err.Error(), "terminal parcel")
}

func TestUnit_StreamAssembler_ErrorParcelStaysSticky(t *testing.T) {
	asm := modelrepo.NewStreamAssembler("provider", "model")
	cause := errors.New("connection reset")
	require.Error(t, asm.Consume(&modelrepo.StreamParcel{Error: cause}))
	require.Error(t, asm.Consume(terminal("stop", true)))

	_, err := asm.Result()
	assert.ErrorIs(t, err, cause)
}
