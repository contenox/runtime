package hooks_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/stretchr/testify/assert"
)

// TestUnitEchoHook_Supports verifies supported hook types
func TestUnitEchoHook_Supports(t *testing.T) {
	hook := hooks.NewEchoHook()
	supported, err := hook.Supports(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, []string{"echo"}, supported)
}

// TestUnitEchoHook_Exec_String tests string input handling
func TestUnitEchoHook_Exec_String(t *testing.T) {
	hook := hooks.NewEchoHook()
	ctx := context.Background()
	start := time.Now()
	transition := "test-transition"
	hookCall := &taskengine.HookCall{}

	t.Run("valid_string", func(t *testing.T) {
		input := "hello world"
		status, output, dt, newTransition, err := hook.Exec(
			ctx, start, input, taskengine.DataTypeString, transition, hookCall,
		)

		assert.NoError(t, err)
		assert.Equal(t, taskengine.StatusSuccess, status)
		assert.Equal(t, input, output)
		assert.Equal(t, taskengine.DataTypeString, dt)
		assert.Equal(t, transition, newTransition)
	})

	t.Run("invalid_string_type", func(t *testing.T) {
		input := 123 // Wrong type
		status, output, dt, newTransition, err := hook.Exec(
			ctx, start, input, taskengine.DataTypeString, transition, hookCall,
		)

		assert.Error(t, err)
		assert.Equal(t, taskengine.StatusError, status)
		assert.Nil(t, output)
		assert.Equal(t, taskengine.DataTypeAny, dt)
		assert.Equal(t, transition, newTransition)
		assert.EqualError(t, err, "invalid string input")
	})
}
