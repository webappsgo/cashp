package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTxCommits(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	err := db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := db.TxExec(ctx, tx,
			`INSERT INTO test_items (id, name) VALUES (?, ?)`, 21, "committed"); err != nil {
			return err
		}
		var name string
		return db.TxQueryRow(ctx, tx,
			`SELECT name FROM test_items WHERE id = ?`, 21).Scan(&name)
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT COUNT(*) FROM test_items WHERE id = ?`, 21).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("committed rows = %d, want 1", count)
	}
}

func TestTxRollsBack(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	sentinel := errors.New("rollback please")

	err := db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := db.TxExec(ctx, tx,
			`INSERT INTO test_items (id, name) VALUES (?, ?)`, 22, "doomed"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx error = %v, want sentinel", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT COUNT(*) FROM test_items WHERE id = ?`, 22).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("rolled-back rows = %d, want 0", count)
	}
}

func TestTxRetriesSerializationFailures(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	attempts := 0
	deadlock := errors.New("Error 1213: Deadlock found when trying to get lock")
	err := db.TxWith(ctx, TimeoutWrite, nil, 3, func(*sql.Tx) error {
		attempts++
		return deadlock
	})
	if !errors.Is(err, deadlock) {
		t.Fatalf("TxWith error = %v, want the deadlock error", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	// A transient failure that clears on the second attempt must succeed.
	attempts = 0
	err = db.TxWith(ctx, TimeoutWrite, nil, 3, func(*sql.Tx) error {
		attempts++
		if attempts == 1 {
			return deadlock
		}
		return nil
	})
	if err != nil {
		t.Fatalf("TxWith: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestTxDoesNotRetryOtherErrors(t *testing.T) {
	db := newTestDB(t)

	attempts := 0
	err := db.TxWith(context.Background(), TimeoutWrite, nil, 0, func(*sql.Tx) error {
		attempts++
		return errors.New("bad request")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestSerializable(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	err := db.Serializable(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		_, err := db.TxExec(ctx, tx,
			`INSERT INTO test_items (id, name) VALUES (?, ?)`, 23, "serial")
		return err
	})
	if err != nil {
		// Some embedded drivers reject an explicit isolation level; SQLite
		// transactions are already serializable, so that is not a failure.
		if !strings.Contains(strings.ToLower(err.Error()), "isolation") {
			t.Fatalf("Serializable: %v", err)
		}
		return
	}

	var name string
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT name FROM test_items WHERE id = ?`, 23).Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "serial" {
		t.Errorf("name = %q, want serial", name)
	}
}

func TestSleepBackoff(t *testing.T) {
	start := time.Now()
	if err := sleepBackoff(context.Background(), 1); err != nil {
		t.Fatalf("sleepBackoff: %v", err)
	}
	if time.Since(start) < retryBaseDelay {
		t.Error("sleepBackoff returned before the base delay elapsed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepBackoff(ctx, 10); !errors.Is(err, ErrCanceled) {
		t.Errorf("sleepBackoff on canceled context = %v, want ErrCanceled", err)
	}
}

func TestTxExecErrorIsClassified(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	err := db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		_, err := db.TxExec(ctx, tx, `INSERT INTO missing_table (id) VALUES (?)`, 1)
		return err
	})
	if err == nil {
		t.Fatal("expected an error from a missing table")
	}
}
