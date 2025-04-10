package libdb

import (
	"context"
	"database/sql"
	"errors"
)

// Errors for common cases.
var (
	// ErrNotFound is returned when no rows were found.
	ErrNotFound = errors.New("libdb: not found")

	// ErrTxFailed indicates that a transaction failed.
	ErrTxFailed = errors.New("libdb: transaction failed")

	// Constraint-related errors.
	ErrUniqueViolation     = errors.New("libdb: unique constraint violation")
	ErrForeignKeyViolation = errors.New("libdb: foreign key violation")
	ErrNotNullViolation    = errors.New("libdb: not null constraint violation")
	ErrCheckViolation      = errors.New("libdb: check constraint violation")

	// Generic constraint violation for cases not handled explicitly.
	ErrConstraintViolation = errors.New("libdb: constraint violation")

	ErrDeadlockDetected     = errors.New("libdb: deadlock detected")
	ErrSerializationFailure = errors.New("libdb: serialization failure")
	ErrLockNotAvailable     = errors.New("libdb: lock not available")
	ErrQueryCanceled        = errors.New("libdb: query canceled")
	ErrDataTruncation       = errors.New("libdb: data truncation error")
	ErrNumericOutOfRange    = errors.New("libdb: numeric value out of range")
	ErrInvalidInputSyntax   = errors.New("libdb: invalid input syntax")
	ErrUndefinedColumn      = errors.New("libdb: undefined column")
	ErrUndefinedTable       = errors.New("libdb: undefined table")
)

// DBManager provides an interface to work with the DB.
type DBManager interface {
	WithoutTransaction() Exec
	WithTransaction(ctx context.Context) (Exec, CommitTx, error)
	Close() error
}

// Exec represents the basic database operations.
type Exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) QueryRower
}

type QueryRower interface {
	Scan(dest ...interface{}) error
}

// CommitTx is a function type to commit a transaction.
type CommitTx func(context.Context) error
