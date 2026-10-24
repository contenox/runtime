package libdbexec

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

const legacySQLiteDSNSuffix = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

func TestBuildSQLiteDSNLegacyVerbatim(t *testing.T) {
	got := buildSQLiteDSN("/var/lib/contenox/local.db", SQLiteOptions{})
	want := "/var/lib/contenox/local.db" + legacySQLiteDSNSuffix
	if got != want {
		t.Fatalf("zero-option DSN drifted\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildSQLiteDSNPreservesExistingQuery(t *testing.T) {
	got := buildSQLiteDSN("file:local.db?mode=rwc", SQLiteOptions{})
	want := "file:local.db?mode=rwc&" + legacySQLiteDSNSuffix[1:]
	if got != want {
		t.Fatalf("DSN with existing query drifted\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildSQLiteDSNWithOptions(t *testing.T) {
	opts := SQLiteOptions{
		Synchronous: SQLiteSynchronousNormal,
		CacheSize:   SQLiteCacheKiB(20000),
	}
	got := buildSQLiteDSN("/db/relay.db", opts)
	want := "/db/relay.db" + legacySQLiteDSNSuffix + "&_pragma=cache_size(-20000)&_pragma=synchronous(NORMAL)"
	if got != want {
		t.Fatalf("options DSN mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildSQLiteDSNOverrides(t *testing.T) {
	opts := SQLiteOptions{
		JournalMode:        SQLiteJournalDelete,
		BusyTimeout:        SQLiteBusyTimeout(250 * time.Millisecond),
		DisableForeignKeys: true,
		CacheSize:          SQLiteCachePages(4000),
	}
	got := buildSQLiteDSN("x.db", opts)
	want := "x.db?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(250)&_pragma=foreign_keys(0)&_pragma=cache_size(4000)"
	if got != want {
		t.Fatalf("override DSN mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestSQLiteCacheHelpersSignConvention(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"KiB is encoded negative", *SQLiteCacheKiB(20000), -20000},
		{"pages are encoded positive", *SQLiteCachePages(2000), 2000},
		{"negative KiB argument stays KiB", *SQLiteCacheKiB(-20000), -20000},
		{"negative pages argument stays pages", *SQLiteCachePages(-2000), 2000},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestSQLiteOptionsValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		opts SQLiteOptions
	}{
		{"normal synchronous outside WAL", SQLiteOptions{
			JournalMode: SQLiteJournalDelete,
			Synchronous: SQLiteSynchronousNormal,
		}},
		{"off synchronous outside WAL", SQLiteOptions{
			JournalMode: SQLiteJournalTruncate,
			Synchronous: SQLiteSynchronousOff,
		}},
		{"normal synchronous with journal off", SQLiteOptions{
			JournalMode: SQLiteJournalOff,
			Synchronous: SQLiteSynchronousNormal,
		}},
		{"unknown journal mode", SQLiteOptions{JournalMode: "ROLLBACK"}},
		{"unknown synchronous level", SQLiteOptions{Synchronous: "NORMLA"}},
		{"zero cache size", SQLiteOptions{CacheSize: SQLiteCachePages(0)}},
		{"negative max open conns", SQLiteOptions{MaxOpenConns: SQLiteConns(-1)}},
		{"negative max idle conns", SQLiteOptions{MaxIdleConns: SQLiteConns(-1)}},
		{"negative busy timeout", SQLiteOptions{BusyTimeout: SQLiteBusyTimeout(-time.Second)}},
		{"unknown tx lock", SQLiteOptions{TxLock: SQLiteTxLock("optimistic")}},
		{"busy timeout rounding to zero", SQLiteOptions{BusyTimeout: SQLiteBusyTimeout(500 * time.Microsecond)}},
		{"negative conn max lifetime", SQLiteOptions{ConnMaxLifetime: -time.Second}},
		{"negative conn max idle time", SQLiteOptions{ConnMaxIdleTime: -time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if !errors.Is(err, ErrInvalidSQLiteOptions) {
				t.Fatalf("error %v does not wrap ErrInvalidSQLiteOptions", err)
			}
		})
	}
}

func TestSQLiteOptionsValidateAccepts(t *testing.T) {
	cases := []struct {
		name string
		opts SQLiteOptions
	}{
		{"zero value", SQLiteOptions{}},
		{"normal synchronous with implicit WAL", SQLiteOptions{Synchronous: SQLiteSynchronousNormal}},
		{"normal synchronous with explicit WAL", SQLiteOptions{
			JournalMode: SQLiteJournalWAL,
			Synchronous: SQLiteSynchronousNormal,
		}},
		{"full synchronous outside WAL", SQLiteOptions{
			JournalMode: SQLiteJournalDelete,
			Synchronous: SQLiteSynchronousFull,
		}},
		{"explicitly unlimited pool", SQLiteOptions{MaxOpenConns: SQLiteConns(0)}},
		{"no idle connections retained", SQLiteOptions{MaxIdleConns: SQLiteConns(0)}},
		{"zero busy timeout fails fast", SQLiteOptions{BusyTimeout: SQLiteBusyTimeout(0)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.opts.validate(); err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

func TestNewSQLiteDBManagerRefusesUnsafeOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.db")
	mgr, err := NewSQLiteDBManagerWithOptions(context.Background(), path, "", SQLiteOptions{
		JournalMode: SQLiteJournalDelete,
		Synchronous: SQLiteSynchronousNormal,
	})
	if err == nil {
		_ = mgr.Close()
		t.Fatal("expected construction to fail for synchronous=NORMAL outside WAL")
	}
	if !errors.Is(err, ErrInvalidSQLiteOptions) {
		t.Fatalf("error %v does not wrap ErrInvalidSQLiteOptions", err)
	}
	if mgr != nil {
		t.Fatal("expected nil DBManager on refusal")
	}
}

func sqlitePragma(t *testing.T, mgr DBManager, name string) string {
	t.Helper()
	var v string
	if err := mgr.WithoutTransaction().QueryRowContext(context.Background(), "pragma "+name).Scan(&v); err != nil {
		t.Fatalf("pragma %s: %v", name, err)
	}
	return v
}

func TestNewSQLiteDBManagerZeroOptionsMatchesLegacy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	legacy, err := NewSQLiteDBManager(ctx, filepath.Join(dir, "legacy.db"), "")
	if err != nil {
		t.Fatalf("legacy constructor: %v", err)
	}
	defer legacy.Close()

	zero, err := NewSQLiteDBManagerWithOptions(ctx, filepath.Join(dir, "zero.db"), "", SQLiteOptions{})
	if err != nil {
		t.Fatalf("options constructor: %v", err)
	}
	defer zero.Close()

	for _, pragma := range []string{"journal_mode", "busy_timeout", "foreign_keys", "synchronous", "cache_size"} {
		if got, want := sqlitePragma(t, zero, pragma), sqlitePragma(t, legacy, pragma); got != want {
			t.Errorf("pragma %s: zero options gave %q, legacy gave %q", pragma, got, want)
		}
	}

	legacyStats := legacy.(*sqliteDBManager).dbInstance.Stats()
	zeroStats := zero.(*sqliteDBManager).dbInstance.Stats()
	if legacyStats.MaxOpenConnections != zeroStats.MaxOpenConnections {
		t.Errorf("MaxOpenConnections: zero options %d, legacy %d",
			zeroStats.MaxOpenConnections, legacyStats.MaxOpenConnections)
	}
	if legacyStats.MaxOpenConnections != 0 {
		t.Errorf("legacy pool is expected to stay unbounded, got MaxOpenConnections=%d", legacyStats.MaxOpenConnections)
	}
}

func TestNewSQLiteDBManagerAppliesPragmas(t *testing.T) {
	mgr, err := NewSQLiteDBManagerWithOptions(context.Background(), filepath.Join(t.TempDir(), "tuned.db"), "", SQLiteOptions{
		Synchronous: SQLiteSynchronousNormal,
		CacheSize:   SQLiteCacheKiB(20000),
		BusyTimeout: SQLiteBusyTimeout(7 * time.Second),
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	defer mgr.Close()

	for _, tc := range []struct{ pragma, want string }{
		{"journal_mode", "wal"},
		{"synchronous", "1"},
		{"cache_size", "-20000"},
		{"busy_timeout", "7000"},
		{"foreign_keys", "1"},
	} {
		if got := sqlitePragma(t, mgr, tc.pragma); got != tc.want {
			t.Errorf("pragma %s = %q, want %q", tc.pragma, got, tc.want)
		}
	}
}

func TestNewSQLiteDBManagerAppliesPoolLimit(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewSQLiteDBManagerWithOptions(ctx, filepath.Join(t.TempDir(), "capped.db"), "", SQLiteOptions{
		MaxOpenConns:    SQLiteConns(1),
		MaxIdleConns:    SQLiteConns(1),
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	defer mgr.Close()

	if got := mgr.(*sqliteDBManager).dbInstance.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}

	_, _, release, err := mgr.WithTransaction(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer release()

	starved, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_, err = mgr.WithoutTransaction().ExecContext(starved, "select 1")
	if !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("expected the capped pool to starve a second caller, got %v", err)
	}
}

func TestNewSQLiteDBManagerUnboundedPoolDoesNotStarve(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "uncapped.db"), "")
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	defer mgr.Close()

	_, _, release, err := mgr.WithTransaction(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer release()

	timed, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := mgr.WithoutTransaction().ExecContext(timed, "select 1"); err != nil {
		t.Fatalf("uncapped pool should serve a concurrent caller, got %v", err)
	}
}

func TestUnit_SQLiteDSN_ImmediateTxLockIsOptIn(t *testing.T) {
	got := buildSQLiteDSN("x.db", SQLiteOptions{TxLock: SQLiteTxLockImmediate})
	want := "x.db?_txlock=immediate&" + legacySQLiteDSNSuffix[1:]
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
