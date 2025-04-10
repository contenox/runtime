package libdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/lib/pq"
)

type postgresDBManager struct {
	dbInstance *sql.DB
}

// NewPostgresDBManager opens the connection, pings, and initializes the schema.
func NewPostgresDBManager(dsn string, schema string) (DBManager, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", translateError(err))
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database connection failed: %w", translateError(err))
	}

	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", translateError(err))
	}

	log.Println("Database connection established and schema verified")
	return &postgresDBManager{dbInstance: db}, nil
}

// WithoutTransaction returns an executor that uses the base DB.
func (sm *postgresDBManager) WithoutTransaction() Exec {
	return &txAwareDB{db: sm.dbInstance}
}

// WithTransaction starts a transaction and returns an executor bound to it along with a commit function.
func (sm *postgresDBManager) WithTransaction(ctx context.Context) (Exec, CommitTx, error) {
	tx, err := sm.dbInstance.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: begin transaction failed: %w", ErrTxFailed, translateError(err))
	}

	store := &txAwareDB{tx: tx}

	commitFn := func(ctx context.Context) error {
		// Check context state before finalizing.
		if ctxErr := ctx.Err(); ctxErr != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				return fmt.Errorf(
					"%w: context error: %v, rollback failed: %v",
					ErrTxFailed,
					translateError(ctxErr),
					translateError(rbErr),
				)
			}
			return fmt.Errorf("%w: %v", ErrTxFailed, ctxErr)
		}

		// Attempt commit.
		if err := tx.Commit(); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				return fmt.Errorf(
					"%w: commit failed: %v, rollback failed: %v",
					ErrTxFailed,
					translateError(err),
					translateError(rbErr),
				)
			}
			return fmt.Errorf("%w: %v", ErrTxFailed, translateError(err))
		}
		return nil
	}

	return store, commitFn, nil
}

// Close shuts down the underlying DB.
func (sm *postgresDBManager) Close() error {
	return sm.dbInstance.Close()
}

// txAwareDB wraps a *sql.DB and/or *sql.Tx to implement Exec.
type txAwareDB struct {
	db *sql.DB
	tx *sql.Tx
}

func (s *txAwareDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if s.tx != nil {
		res, err := s.tx.ExecContext(ctx, query, args...)
		return res, translateError(err)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	return res, translateError(err)
}

func (s *txAwareDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s.tx != nil {
		rows, err := s.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, translateError(err)
		}
		return rows, nil
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, translateError(err)
	}

	return rows, nil
}

func (s *txAwareDB) QueryRowContext(ctx context.Context, query string, args ...any) QueryRower {
	var r *sql.Row
	if s.tx != nil {
		r = s.tx.QueryRowContext(ctx, query, args...)
	} else {
		r = s.db.QueryRowContext(ctx, query, args...)
	}
	return &row{inner: r}
}

// row wraps *sql.Row and implements QueryRower.
type row struct {
	inner *sql.Row
}

// Scan calls the underlying Scan and translates the error.
func (r *row) Scan(dest ...interface{}) error {
	err := r.inner.Scan(dest...)
	return translateError(err)
}

// translateError translates raw errors into our package-specific errors.
func translateError(err error) error {
	if err == nil {
		return nil
	}

	// Handle no rows error.
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	// Check if it's a pq error.
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return ErrUniqueViolation
		case "23503":
			return ErrForeignKeyViolation
		case "23502":
			return ErrNotNullViolation
		case "23514":
			return ErrCheckViolation
		case "40P01":
			return ErrDeadlockDetected
		case "40001":
			return ErrSerializationFailure
		case "55P03":
			return ErrLockNotAvailable
		case "57014":
			return ErrQueryCanceled
		case "22001":
			return ErrDataTruncation
		case "22003":
			return ErrNumericOutOfRange
		case "42703":
			return ErrUndefinedColumn
		case "42P01":
			return ErrUndefinedTable
		default:
			return ErrConstraintViolation
		}
	}

	return fmt.Errorf("libdb: unexpected error: %w", err)
}
