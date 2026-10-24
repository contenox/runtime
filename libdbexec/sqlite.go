package libdbexec

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type sqliteDBManager struct {
	dbInstance *sql.DB
}

func NewSQLiteDBManager(ctx context.Context, path string, schema string) (DBManager, error) {
	return NewSQLiteDBManagerWithOptions(ctx, path, schema, SQLiteOptions{})
}

func NewSQLiteDBManagerWithOptions(ctx context.Context, path string, schema string, opts SQLiteOptions) (DBManager, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if err := ensureSQLiteParentDir(path); err != nil {
		return nil, fmt.Errorf("sqlite parent dir: %w", err)
	}
	dsnPath := path
	if path == ":memory:" {
		dsnPath = "file:libdbexec-" + rand.Text() + "?mode=memory&cache=shared"
	}
	dsn := buildSQLiteDSN(dsnPath, opts)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", translateSQLiteError(err))
	}
	applySQLitePool(db, opts)

	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite connection failed: %w", translateSQLiteError(err))
	}

	if schema != "" {
		for _, stmt := range splitSQLStatements(schema) {
			if _, err = db.ExecContext(ctx, stmt); err != nil {
				msg := err.Error()
				if strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists") {
					continue
				}
				_ = db.Close()
				return nil, fmt.Errorf("failed to initialize sqlite schema (stmt: %q): %w", stmt, translateSQLiteError(err))
			}
		}
	}

	return &sqliteDBManager{dbInstance: db}, nil
}

func splitSQLStatements(script string) []string {
	var stmts []string
	var cur strings.Builder
	inString, inLineComment, inBlockComment := false, false, false
	var quoteChar byte

	for i := 0; i < len(script); i++ {
		c := script[i]

		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			cur.WriteByte(c)
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(script) && script[i+1] == '/' {
				inBlockComment = false
				cur.WriteString("*/")
				i++
			} else {
				cur.WriteByte(c)
			}
			continue
		}

		if !inString {
			if c == '-' && i+1 < len(script) && script[i+1] == '-' {
				inLineComment = true
				cur.WriteString("--")
				i++
				continue
			}
			if c == '/' && i+1 < len(script) && script[i+1] == '*' {
				inBlockComment = true
				cur.WriteString("/*")
				i++
				continue
			}
		}

		if c == '\'' || c == '"' || c == '`' {
			if !inString {
				inString = true
				quoteChar = c
			} else if c == quoteChar {
				if i+1 < len(script) && script[i+1] == quoteChar {
					cur.WriteByte(c)
					cur.WriteByte(script[i+1])
					i++
					continue
				}
				inString = false
			}
		}

		if !inString && c == ';' {
			if stmt := strings.TrimSpace(cur.String()); stmt != "" {
				stmts = append(stmts, stmt)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if stmt := strings.TrimSpace(cur.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

func (sm *sqliteDBManager) WithoutTransaction() Exec {
	return &txAwareDB{db: sm.dbInstance, errTranslate: translateSQLiteError, driverName: "sqlite"}
}

func (sm *sqliteDBManager) WithTransaction(ctx context.Context, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error) {
	return sm.WithTransactionOptions(ctx, nil, onRollback...)
}

func (sm *sqliteDBManager) WithTransactionOptions(ctx context.Context, opts *sql.TxOptions, onRollback ...func()) (Exec, CommitTx, ReleaseTx, error) {
	tx, err := sm.dbInstance.BeginTx(ctx, opts)
	if err != nil {
		return nil, nil, func() error { return nil }, fmt.Errorf("%w: begin transaction failed: %w", ErrTxFailed, translateSQLiteError(err))
	}

	readOnly := opts != nil && opts.ReadOnly
	if readOnly {
		if _, err := tx.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			_ = tx.Rollback()
			return nil, nil, func() error { return nil }, fmt.Errorf("%w: read-only mode failed: %w", ErrTxFailed, translateSQLiteError(err))
		}
	}
	restoreWrites := func() error {
		if !readOnly {
			return nil
		}
		readOnly = false
		if _, err := tx.ExecContext(context.WithoutCancel(ctx), "PRAGMA query_only = OFF"); err != nil {
			return fmt.Errorf("%w: restoring write mode failed: %w", ErrTxFailed, translateSQLiteError(err))
		}
		return nil
	}

	store := &txAwareDB{tx: tx, errTranslate: translateSQLiteError, driverName: "sqlite"}
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
		if err := restoreWrites(); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: commit failed: %w", ErrTxFailed, translateSQLiteError(err))
		}
		finalized = true
		return nil
	}

	releaseFn := func() error {
		var restoreErr error
		if !finalized {
			restoreErr = restoreWrites()
		}
		rollbackErr := tx.Rollback()
		if !finalized {
			finalized = true
			fireRollback()
		}
		if restoreErr != nil {
			return restoreErr
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("%w: rollback failed: %w", ErrTxFailed, translateSQLiteError(rollbackErr))
		}
		return nil
	}

	return store, commitFn, releaseFn, nil
}

func (sm *sqliteDBManager) Close() error {
	if sm.dbInstance != nil {
		return sm.dbInstance.Close()
	}
	return nil
}

func translateSQLiteError(err error) error {
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
	s := err.Error()
	if strings.Contains(s, "UNIQUE constraint") {
		return fmt.Errorf("%w: %w", ErrUniqueViolation, err)
	}
	if strings.Contains(s, "FOREIGN KEY constraint") {
		return fmt.Errorf("%w: %w", ErrForeignKeyViolation, err)
	}
	if strings.Contains(s, "NOT NULL constraint") {
		return fmt.Errorf("%w: %w", ErrNotNullViolation, err)
	}
	if strings.Contains(s, "CHECK constraint") {
		return fmt.Errorf("%w: %w", ErrCheckViolation, err)
	}
	return fmt.Errorf("libdb: sqlite error: %w", err)
}

func ensureSQLiteParentDir(path string) error {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file::memory") {
		return nil
	}
	fsPath := path
	if strings.HasPrefix(fsPath, "file:") {
		fsPath = strings.TrimPrefix(fsPath, "file:")
		if before, _, ok := strings.Cut(fsPath, "?"); ok {
			fsPath = before
		}
	}
	dir := filepath.Dir(fsPath)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
