package runtimetypes

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/libdbexec"
)

// BeginWrite reserves SQLite's writer when it is the first statement in an
// open transaction, before that transaction reads. The no-op delete leaves
// every KV row unchanged; other database backends receive no stronger
// serialization guarantee.
func BeginWrite(ctx context.Context, exec libdbexec.Exec) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM kv WHERE 1 = 0`); err != nil {
		return fmt.Errorf("runtime store: begin write: %w", err)
	}
	return nil
}
