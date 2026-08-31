package database

import (
	"context"
	"database/sql"
	"time"
)

// Retry policy for serialization / deadlock / busy-writer failures
// (PART 10 -> "Retry on Serialization Failure").
const (
	// DefaultMaxRetries is how many times a transaction is replayed before
	// giving up.
	DefaultMaxRetries = 5
	// retryBaseDelay is the first backoff step; each attempt doubles it.
	retryBaseDelay = 10 * time.Millisecond
	// retryMaxDelay caps the exponential backoff.
	retryMaxDelay = 500 * time.Millisecond
)

// Tx runs fn inside a transaction under the given timeout, committing on
// success and rolling back on any error. Serialization and write-conflict
// failures are retried with exponential backoff.
func (db *DB) Tx(ctx context.Context, timeout time.Duration, fn func(*sql.Tx) error) error {
	return db.TxWith(ctx, timeout, nil, DefaultMaxRetries, fn)
}

// Serializable runs fn in a SERIALIZABLE transaction with retry, for the
// read-check-write flows that must not interleave (PART 10 -> "Serializable
// Isolation").
func (db *DB) Serializable(ctx context.Context, timeout time.Duration, fn func(*sql.Tx) error) error {
	return db.TxWith(ctx, timeout, &sql.TxOptions{Isolation: sql.LevelSerializable}, DefaultMaxRetries, fn)
}

// TxWith runs fn in a transaction with explicit options and retry budget.
// A maxRetries below 1 is treated as a single attempt.
func (db *DB) TxWith(ctx context.Context, timeout time.Duration, opts *sql.TxOptions, maxRetries int, fn func(*sql.Tx) error) error {
	if maxRetries < 1 {
		maxRetries = 1
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, attempt); err != nil {
				return err
			}
		}

		err := db.runTx(ctx, timeout, opts, fn)
		if err == nil {
			return nil
		}
		if !IsSerializationError(err) {
			return err
		}
		lastErr = err
	}

	if lastErr != nil {
		return lastErr
	}
	return ErrMaxRetries
}

// runTx executes a single transaction attempt.
func (db *DB) runTx(ctx context.Context, timeout time.Duration, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	tctx, cancel := context.WithTimeout(ctx, resolveTimeout(timeout))
	defer cancel()

	tx, err := db.db.BeginTx(tctx, opts)
	if err != nil {
		return Classify(err)
	}

	if err := fn(tx); err != nil {
		// Rollback failure is subordinate to the original error.
		_ = tx.Rollback()
		return Classify(err)
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return Classify(err)
	}
	return nil
}

// TxExec runs a statement inside an existing transaction, applying the
// driver's placeholder dialect.
func (db *DB) TxExec(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	res, err := tx.ExecContext(ctx, db.Rebind(query), args...)
	if err != nil {
		return nil, Classify(err)
	}
	return res, nil
}

// TxQueryRow runs a single-row query inside an existing transaction,
// applying the driver's placeholder dialect.
func (db *DB) TxQueryRow(ctx context.Context, tx *sql.Tx, query string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, db.Rebind(query), args...)
}

// sleepBackoff waits out the exponential backoff for the given attempt,
// aborting early if the caller's context is done.
func sleepBackoff(ctx context.Context, attempt int) error {
	delay := retryBaseDelay << (attempt - 1)
	if delay > retryMaxDelay || delay <= 0 {
		delay = retryMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Classify(ctx.Err())
	case <-timer.C:
		return nil
	}
}
