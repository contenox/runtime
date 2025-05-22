package libtestenv_test

import (
	"context"
	"testing"

	"github.com/contenox/contenox/libs/libtestenv"
	"github.com/stretchr/testify/require"
)

func TestStartupWorkerInstance(t *testing.T) {
	ctx := context.TODO()
	_, cleanup, err := libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{})
	t.Cleanup(func() {
		cleanup()
	})
	require.NoError(t, err)
}
