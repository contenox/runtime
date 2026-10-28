package hitlservice_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Evaluate_WhitespaceOnlyCommandDoesNotPanic(t *testing.T) {
	t.Parallel()
	blacklist := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":"rm,mkfs"}]}`)
	askAlways := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"command","op":"command_ask_always","value":"rm,sudo"}]}`)

	for _, cmd := range []string{" ", "/ ", "\t", "/\t \n"} {
		args := map[string]any{"command": cmd}
		var r hitlservice.EvaluationResult
		var err error
		require.NotPanics(t, func() {
			r, err = blacklist.Evaluate(context.Background(), "local_shell", "local_shell", args)
		}, "command_blacklist with %q", cmd)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionAllow, r.Action, "whitespace-only command %q must not match a blacklist rule", cmd)

		require.NotPanics(t, func() {
			r, err = askAlways.Evaluate(context.Background(), "local_shell", "local_shell", args)
		}, "command_ask_always with %q", cmd)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionAllow, r.Action, "whitespace-only command %q must not match an ask-always rule", cmd)
	}
}
