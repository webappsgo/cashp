package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Classify maps a raw driver/context error onto the package's sentinel
// errors (PART 10 -> "Handling Timeouts"). The original error is always
// wrapped so debug builds can still surface the driver detail; callers must
// never render the wrapped text in an HTTP response (PART 11, Tier 1).
func Classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return errors.Join(ErrTimeout, err)
	case errors.Is(err, context.Canceled):
		return errors.Join(ErrCanceled, err)
	case errors.Is(err, sql.ErrNoRows):
		return errors.Join(ErrNotFound, err)
	default:
		return err
	}
}

// IsNotFound reports whether the error means "no rows matched".
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// IsTimeout reports whether the error is a query timeout.
func IsTimeout(err error) bool {
	return errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded)
}

// IsConflict reports whether an optimistic-locking update lost its race.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsSerializationError reports whether the error is a retryable
// serialization / deadlock / busy-writer failure.
//
// PostgreSQL 40001 serialization_failure, PostgreSQL 40P01 deadlock_detected,
// MySQL 1213 deadlock found, MySQL 1205 lock wait timeout, SQL Server 1205
// deadlock victim, and SQLite's SQLITE_BUSY/SQLITE_LOCKED all qualify.
func IsSerializationError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		"40001",
		"40p01",
		"1213",
		"1205",
		"serialization failure",
		"deadlock",
		"database is locked",
		"database table is locked",
		"sqlite_busy",
		"could not serialize access",
	}
	for _, needle := range needles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// IsAlreadyExistsError reports whether a schema statement failed only
// because the column, table or index it creates is already present. Those
// are the errors EnsureSchema tolerates so schema application stays
// idempotent on drivers without IF NOT EXISTS support.
func IsAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		// SQLite / libSQL
		"duplicate column",
		// PostgreSQL
		"already exists",
		// MySQL 1060 / 1061 / 1050
		"duplicate column name",
		"duplicate key name",
		"already exists;",
		// SQL Server 2705 / 1913
		"already an object named",
		"column names in each table must be unique",
		"the operation failed because an index or statistics with name",
	}
	for _, needle := range needles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
