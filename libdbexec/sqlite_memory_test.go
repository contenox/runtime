package libdbexec_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/contenox/runtime/libdbexec"
)

func TestUnit_SQLiteMemory_ConnectionsShareOneDatabase(t *testing.T) {
	ctx := context.Background()
	m, err := libdbexec.NewSQLiteDBManager(ctx, ":memory:", "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	tx, commit, release, err := m.WithTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := tx.ExecContext(ctx, "INSERT INTO t (v) VALUES ('held')"); err != nil {
		t.Fatal(err)
	}

	other, err := libdbexec.NewSQLiteDBManager(ctx, ":memory:", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := other.WithoutTransaction().ExecContext(ctx, "SELECT 1 FROM t"); err == nil {
		t.Fatal("second manager sees the first manager's table")
	}

	if err := commit(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := m.WithoutTransaction().QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatalf("count = %d, err = %v", n, err)
	}
}

func TestUnit_SQLiteMemory_ConcurrentConnectionsSeeSameTable(t *testing.T) {
	ctx := context.Background()
	m, err := libdbexec.NewSQLiteDBManager(ctx, ":memory:", "CREATE TABLE t (id INTEGER PRIMARY KEY);")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	readTx, _, releaseRead, err := m.WithTransactionOptions(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRead()
	if _, err := readTx.ExecContext(ctx, "SELECT COUNT(*) FROM t"); err != nil {
		t.Fatalf("held connection cannot see the table: %v", err)
	}
	if _, err := m.WithoutTransaction().ExecContext(ctx, "SELECT COUNT(*) FROM t"); err != nil {
		t.Fatalf("second connection cannot see the table: %v", err)
	}
}

func TestUnit_SQLite_ReadOnlyTransactionRefusesWrite(t *testing.T) {
	ctx := context.Background()
	m, err := libdbexec.NewSQLiteDBManager(ctx, ":memory:", "CREATE TABLE t (id INTEGER PRIMARY KEY);")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	tx, _, release, err := m.WithTransactionOptions(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = tx.ExecContext(ctx, "INSERT INTO t DEFAULT VALUES")
	if err == nil {
		t.Fatal("write inside a read-only transaction succeeded")
	}
	if errors.Is(err, libdbexec.ErrNotFound) {
		t.Fatalf("unexpected classification: %v", err)
	}
}

func TestUnit_SQLite_ReadOnlyTransactionLeavesPoolWritable(t *testing.T) {
	ctx := context.Background()
	m, err := libdbexec.NewSQLiteDBManager(ctx, ":memory:", "CREATE TABLE t (id INTEGER PRIMARY KEY);")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	for _, finish := range []string{"commit", "release"} {
		tx, commit, release, err := m.WithTransactionOptions(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if finish == "commit" {
			if err := commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		_ = tx
		if err := release(); err != nil {
			t.Fatal(err)
		}
		if _, err := m.WithoutTransaction().ExecContext(ctx, "INSERT INTO t DEFAULT VALUES"); err != nil {
			t.Fatalf("pool stayed read-only after %s: %v", finish, err)
		}
	}
}
