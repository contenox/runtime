package libdbexec

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidSQLiteOptions = errors.New("libdb: invalid sqlite options")

type SQLiteJournalMode string

const (
	SQLiteJournalDefault  SQLiteJournalMode = ""
	SQLiteJournalWAL      SQLiteJournalMode = "WAL"
	SQLiteJournalDelete   SQLiteJournalMode = "DELETE"
	SQLiteJournalTruncate SQLiteJournalMode = "TRUNCATE"
	SQLiteJournalPersist  SQLiteJournalMode = "PERSIST"
	SQLiteJournalMemory   SQLiteJournalMode = "MEMORY"
	SQLiteJournalOff      SQLiteJournalMode = "OFF"
)

type SQLiteSynchronous string

const (
	SQLiteSynchronousDefault SQLiteSynchronous = ""
	SQLiteSynchronousOff     SQLiteSynchronous = "OFF"
	SQLiteSynchronousNormal  SQLiteSynchronous = "NORMAL"
	SQLiteSynchronousFull    SQLiteSynchronous = "FULL"
	SQLiteSynchronousExtra   SQLiteSynchronous = "EXTRA"
)

const defaultSQLiteBusyTimeout = 5000 * time.Millisecond

type SQLiteOptions struct {
	JournalMode SQLiteJournalMode

	TxLock SQLiteTxLock

	Synchronous SQLiteSynchronous

	BusyTimeout *time.Duration

	DisableForeignKeys bool

	CacheSize *int

	MaxOpenConns *int

	MaxIdleConns *int

	ConnMaxLifetime time.Duration

	ConnMaxIdleTime time.Duration
}

func SQLiteCacheKiB(kib int) *int {
	v := -abs(kib)
	return &v
}

func SQLiteCachePages(pages int) *int {
	v := abs(pages)
	return &v
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func SQLiteConns(n int) *int {
	return &n
}

func SQLiteBusyTimeout(d time.Duration) *time.Duration {
	return &d
}

var validSQLiteJournalModes = map[SQLiteJournalMode]bool{
	SQLiteJournalWAL:      true,
	SQLiteJournalDelete:   true,
	SQLiteJournalTruncate: true,
	SQLiteJournalPersist:  true,
	SQLiteJournalMemory:   true,
	SQLiteJournalOff:      true,
}

var validSQLiteSynchronous = map[SQLiteSynchronous]bool{
	SQLiteSynchronousOff:    true,
	SQLiteSynchronousNormal: true,
	SQLiteSynchronousFull:   true,
	SQLiteSynchronousExtra:  true,
}

func (o SQLiteOptions) journalMode() SQLiteJournalMode {
	if o.JournalMode == SQLiteJournalDefault {
		return SQLiteJournalWAL
	}
	return o.JournalMode
}

func (o SQLiteOptions) busyTimeout() time.Duration {
	if o.BusyTimeout == nil {
		return defaultSQLiteBusyTimeout
	}
	return *o.BusyTimeout
}

func (o SQLiteOptions) validate() error {
	if o.JournalMode != SQLiteJournalDefault && !validSQLiteJournalModes[o.JournalMode] {
		return fmt.Errorf("%w: unknown journal mode %q", ErrInvalidSQLiteOptions, o.JournalMode)
	}
	if o.Synchronous != SQLiteSynchronousDefault && !validSQLiteSynchronous[o.Synchronous] {
		return fmt.Errorf("%w: unknown synchronous level %q", ErrInvalidSQLiteOptions, o.Synchronous)
	}
	if o.Synchronous == SQLiteSynchronousNormal || o.Synchronous == SQLiteSynchronousOff {
		if mode := o.journalMode(); mode != SQLiteJournalWAL {
			return fmt.Errorf("%w: synchronous=%s requires journal_mode=WAL, got %s: reduced synchronous levels risk corruption in rollback journal modes",
				ErrInvalidSQLiteOptions, o.Synchronous, mode)
		}
	}
	if o.BusyTimeout != nil {
		d := *o.BusyTimeout
		if d < 0 {
			return fmt.Errorf("%w: busy timeout %s is negative", ErrInvalidSQLiteOptions, d)
		}
		if d > 0 && d.Milliseconds() == 0 {
			return fmt.Errorf("%w: busy timeout %s rounds to 0ms, which disables waiting entirely", ErrInvalidSQLiteOptions, d)
		}
	}
	if o.CacheSize != nil && *o.CacheSize == 0 {
		return fmt.Errorf("%w: cache size 0 disables the page cache; leave CacheSize nil to keep SQLite's default", ErrInvalidSQLiteOptions)
	}
	if o.MaxOpenConns != nil && *o.MaxOpenConns < 0 {
		return fmt.Errorf("%w: max open conns %d is negative", ErrInvalidSQLiteOptions, *o.MaxOpenConns)
	}
	if o.MaxIdleConns != nil && *o.MaxIdleConns < 0 {
		return fmt.Errorf("%w: max idle conns %d is negative", ErrInvalidSQLiteOptions, *o.MaxIdleConns)
	}
	if o.ConnMaxLifetime < 0 {
		return fmt.Errorf("%w: conn max lifetime %s is negative", ErrInvalidSQLiteOptions, o.ConnMaxLifetime)
	}
	if o.ConnMaxIdleTime < 0 {
		return fmt.Errorf("%w: conn max idle time %s is negative", ErrInvalidSQLiteOptions, o.ConnMaxIdleTime)
	}
	switch o.TxLock {
	case SQLiteTxLockDefault, SQLiteTxLockImmediate, SQLiteTxLockDeferred, SQLiteTxLockExclusive:
	default:
		return fmt.Errorf("%w: tx lock %q is not deferred, immediate or exclusive", ErrInvalidSQLiteOptions, o.TxLock)
	}
	return nil
}

type SQLiteTxLock string

const (
	SQLiteTxLockDefault   SQLiteTxLock = ""
	SQLiteTxLockImmediate SQLiteTxLock = "immediate"
	SQLiteTxLockDeferred  SQLiteTxLock = "deferred"
	SQLiteTxLockExclusive SQLiteTxLock = "exclusive"
)

func buildSQLiteDSN(path string, opts SQLiteOptions) string {
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?"
	} else {
		dsn += "&"
	}
	if opts.TxLock != SQLiteTxLockDefault {
		dsn += "_txlock=" + string(opts.TxLock) + "&"
	}
	pragmas := []string{
		fmt.Sprintf("journal_mode(%s)", opts.journalMode()),
		fmt.Sprintf("busy_timeout(%d)", opts.busyTimeout().Milliseconds()),
		fmt.Sprintf("foreign_keys(%d)", boolPragma(!opts.DisableForeignKeys)),
	}
	if opts.CacheSize != nil {
		pragmas = append(pragmas, fmt.Sprintf("cache_size(%d)", *opts.CacheSize))
	}
	if opts.Synchronous != SQLiteSynchronousDefault {
		pragmas = append(pragmas, fmt.Sprintf("synchronous(%s)", opts.Synchronous))
	}
	for i, p := range pragmas {
		if i > 0 {
			dsn += "&"
		}
		dsn += "_pragma=" + p
	}
	return dsn
}

func boolPragma(v bool) int {
	if v {
		return 1
	}
	return 0
}

func applySQLitePool(db *sql.DB, opts SQLiteOptions) {
	if opts.MaxOpenConns != nil {
		db.SetMaxOpenConns(*opts.MaxOpenConns)
	}
	if opts.MaxIdleConns != nil {
		db.SetMaxIdleConns(*opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}
	if opts.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(opts.ConnMaxIdleTime)
	}
}
