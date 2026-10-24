package libdbexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

type postgresDBManager struct {
	dbInstance *sql.DB
}

func NewPostgresDBManager(ctx context.Context, dsn string, schema string) (DBManager, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", translatePostgresError(err))
	}

	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database connection failed: %w", translatePostgresError(err))
	}

	if schema != "" {
		if _, err = db.ExecContext(ctx, schema); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to initialize schema: %w", translatePostgresError(err))
		}
	}

	return &postgresDBManager{dbInstance: db}, nil
}

func (sm *postgresDBManager) WithoutTransaction() Exec {
	return &txAwareDB{db: sm.dbInstance, errTranslate: translatePostgresError, driverName: "postgres"}
}

func (sm *postgresDBManager) WithTransaction(ctx context.Context, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error) {
	return sm.WithTransactionOptions(ctx, nil, onRollback...)
}

func (sm *postgresDBManager) WithTransactionOptions(ctx context.Context, opts *sql.TxOptions, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error) {
	tx, err := sm.dbInstance.BeginTx(ctx, opts)
	if err != nil {
		return nil, nil, func() error { return nil }, fmt.Errorf("%w: begin transaction failed: %w", ErrTxFailed, translatePostgresError(err))
	}

	store := &txAwareDB{tx: tx, errTranslate: translatePostgresError, driverName: "postgres"}
	finalized := false
	fireRollback := func() {
		for _, f := range onRollback {
			if f != nil {
				f()
			}
		}
		onRollback = nil
	}

	commitFn := func(commitCtx context.Context) error {
		if ctxErr := commitCtx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: context error before commit: %w", ErrTxFailed, ctxErr)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: commit failed: %w", ErrTxFailed, translatePostgresError(err))
		}
		finalized = true
		return nil
	}

	releaseFn := func() error {
		rollbackErr := tx.Rollback()
		if !finalized {
			finalized = true
			fireRollback()
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("%w: rollback failed: %w", ErrTxFailed, translatePostgresError(rollbackErr))
		}
		return nil
	}

	return store, commitFn, releaseFn, nil
}

func (sm *postgresDBManager) Close() error {
	if sm.dbInstance != nil {
		return sm.dbInstance.Close()
	}
	return nil
}

func translatePostgresError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}

	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrQueryCanceled, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrQueryCanceled, context.DeadlineExceeded)
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return fmt.Errorf("%w: %w", ErrUniqueViolation, err)
		case "23503":
			return fmt.Errorf("%w: %w", ErrForeignKeyViolation, err)
		case "23502":
			return fmt.Errorf("%w: %w", ErrNotNullViolation, err)
		case "23514":
			return fmt.Errorf("%w: %w", ErrCheckViolation, err)
		case "40P01":
			return fmt.Errorf("%w: %w", ErrDeadlockDetected, err)
		case "40001":
			return fmt.Errorf("%w: %w", ErrSerializationFailure, err)
		case "55P03":
			return fmt.Errorf("%w: %w", ErrLockNotAvailable, err)
		case "57014":
			return fmt.Errorf("%w: %w", ErrQueryCanceled, err)
		case "22001":
			return fmt.Errorf("%w: %w", ErrDataTruncation, err)
		case "22003":
			return fmt.Errorf("%w: %w", ErrNumericOutOfRange, err)
		case "22P02":
			return fmt.Errorf("%w: %w", ErrInvalidInputSyntax, err)
		case "42703":
			return fmt.Errorf("%w: %w", ErrUndefinedColumn, err)
		case "42P01":
			return fmt.Errorf("%w: %w", ErrUndefinedTable, err)
		default:
			if pqErr.Code.Class() == "23" {
				return fmt.Errorf("%w: %w", ErrConstraintViolation, err)
			}
			return fmt.Errorf("libdb: postgres error: code=%s detail=%q message=%q: %w",
				pqErr.Code, pqErr.Detail, pqErr.Message, err)
		}
	}

	return fmt.Errorf("libdb: unexpected database error: %w", err)
}
