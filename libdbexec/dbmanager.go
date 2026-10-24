package libdbexec

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrNotFound = errors.New("libdb: not found")

	ErrTxFailed = errors.New("libdb: transaction failed")

	ErrMaxRowsReached = errors.New("max row count reached")

	ErrUniqueViolation     = errors.New("libdb: unique constraint violation")
	ErrForeignKeyViolation = errors.New("libdb: foreign key violation")
	ErrNotNullViolation    = errors.New("libdb: not null constraint violation")
	ErrCheckViolation      = errors.New("libdb: check constraint violation")
	ErrConstraintViolation = errors.New("libdb: constraint violation")

	ErrDeadlockDetected     = errors.New("libdb: deadlock detected")
	ErrSerializationFailure = errors.New("libdb: serialization failure")
	ErrLockNotAvailable     = errors.New("libdb: lock not available")
	ErrQueryCanceled        = errors.New("libdb: query canceled")

	ErrDataTruncation     = errors.New("libdb: data truncation error")
	ErrNumericOutOfRange  = errors.New("libdb: numeric value out of range")
	ErrInvalidInputSyntax = errors.New("libdb: invalid input syntax")

	ErrUndefinedColumn = errors.New("libdb: undefined column")
	ErrUndefinedTable  = errors.New("libdb: undefined table")
)

type DBManager interface {
	WithoutTransaction() Exec

	WithTransaction(ctx context.Context, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error)

	WithTransactionOptions(ctx context.Context, opts *sql.TxOptions, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error)

	Close() error
}

type Exec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)

	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)

	QueryRowContext(ctx context.Context, query string, args ...any) QueryRower

	DriverName() string
}

type QueryRower interface {
	Scan(dest ...any) error
}

type CommitTx func(ctx context.Context) error

type ReleaseTx func() error

type txAwareDB struct {
	db           *sql.DB
	tx           *sql.Tx
	errTranslate func(error) error
	driverName   string
}

func (s *txAwareDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var res sql.Result
	var err error
	if s.tx != nil {
		res, err = s.tx.ExecContext(ctx, query, args...)
	} else if s.db != nil {
		res, err = s.db.ExecContext(ctx, query, args...)
	} else {
		return nil, errors.New("libdb: Exec called on uninitialized txAwareDB")
	}
	return res, s.errTranslate(err)
}

func (s *txAwareDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	var rows *sql.Rows
	var err error
	if s.tx != nil {
		rows, err = s.tx.QueryContext(ctx, query, args...)
	} else if s.db != nil {
		rows, err = s.db.QueryContext(ctx, query, args...)
	} else {
		return nil, errors.New("libdb: Query called on uninitialized txAwareDB")
	}
	if err != nil {
		return nil, s.errTranslate(err)
	}
	return rows, nil
}

func (s *txAwareDB) QueryRowContext(ctx context.Context, query string, args ...any) QueryRower {
	var r *sql.Row
	if s.tx != nil {
		r = s.tx.QueryRowContext(ctx, query, args...)
	} else if s.db != nil {
		r = s.db.QueryRowContext(ctx, query, args...)
	} else {
		return &row{err: errors.New("libdb: QueryRow called on uninitialized txAwareDB")}
	}
	return &row{inner: r, errTranslate: s.errTranslate}
}

func (s *txAwareDB) DriverName() string {
	return s.driverName
}

type row struct {
	inner        *sql.Row
	err          error
	errTranslate func(error) error
}

func (r *row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.inner == nil {
		return errors.New("libdb: Scan called on nil row wrapper")
	}
	return r.errTranslate(r.inner.Scan(dest...))
}
