package supervisor_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_CooperativeYieldPreservesRunningGeneration(t *testing.T) {
	fixture := sharedOwner(t)
	chatClient, completionClient := connectOwner(t, fixture), connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	policy := admission.DefaultConfig()
	policy.GlobalLimit = 1
	require.NoError(t, chatClient.ConfigureAdmission(ctx, policy))
	request := roleRequest(fixture, "primary")
	request.Config.NumCtx = 256
	chat, err := chatClient.OpenSession(ctx, request)
	require.NoError(t, err)
	defer chat.Close()
	chunks, err := chat.Decode(ctx, transport.DecodeConfig{MaxTokens: 888888})
	require.NoError(t, err)
	var generated atomic.Int64
	drained := make(chan error, 1)
	go func() {
		var failure error
		for chunk := range chunks {
			if chunk.Error != nil {
				failure = chunk.Error
				continue
			}
			if chunk.Text == "" {
				continue
			}
			number, err := strconv.Atoi(chunk.Text)
			if err != nil {
				failure = err
				continue
			}
			if number != int(generated.Load()) {
				failure = admission.ErrInvalidPermit
			}
			generated.Add(1)
		}
		drained <- failure
	}()
	require.Eventually(t, func() bool { return generated.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
	before, err := chatClient.Admission(ctx)
	require.NoError(t, err)
	var chatPermit admission.Permit
	for _, worker := range before.Workers {
		if worker.Active != nil {
			chatPermit = *worker.Active
		}
	}
	completionRequest := roleRequest(fixture, "autocomplete")
	completionRequest.Config.Execution.Priority = string(admission.Autocomplete)
	completionRequest.Config.NumCtx = 512
	completion, err := completionClient.OpenSession(ctx, completionRequest)
	require.NoError(t, err)
	state, err := chatClient.Admission(ctx)
	require.NoError(t, err)
	paused := false
	for _, worker := range state.Workers {
		if worker.ID == chatPermit.WorkerID {
			require.Equal(t, chatPermit, *worker.Active)
			paused = worker.Suspended
		}
	}
	require.True(t, paused)
	require.Len(t, state.Resources.Allocations, 2)
	_, err = completion.EnsurePrefix(ctx, transport.PrefixInput{Text: "finish editor line"})
	require.NoError(t, err)
	atPause := generated.Load()
	require.NoError(t, completion.Close())
	require.Eventually(t, func() bool { return generated.Load() > atPause+2 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, chat.Close())
	select {
	case err := <-drained:
		require.NotErrorIs(t, err, admission.ErrInvalidPermit)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
