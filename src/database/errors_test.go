package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestClassify(t *testing.T) {
	if Classify(nil) != nil {
		t.Error("Classify(nil) must be nil")
	}

	if err := Classify(context.DeadlineExceeded); !IsTimeout(err) {
		t.Errorf("deadline exceeded classified as %v", err)
	}
	if err := Classify(context.Canceled); !errors.Is(err, ErrCanceled) {
		t.Errorf("canceled classified as %v", err)
	}
	if err := Classify(sql.ErrNoRows); !IsNotFound(err) {
		t.Errorf("no rows classified as %v", err)
	}

	other := errors.New("boom")
	if err := Classify(other); !errors.Is(err, other) {
		t.Errorf("unknown error was rewritten: %v", err)
	}
}

func TestIsSerializationError(t *testing.T) {
	retryable := []error{
		errors.New("ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)"),
		errors.New("SQLSTATE 40P01: deadlock detected"),
		errors.New("Error 1213: Deadlock found when trying to get lock"),
		errors.New("Error 1205: Lock wait timeout exceeded"),
		errors.New("database is locked (SQLITE_BUSY)"),
	}
	for _, err := range retryable {
		if !IsSerializationError(err) {
			t.Errorf("expected retryable: %v", err)
		}
	}

	if IsSerializationError(nil) {
		t.Error("nil must not be retryable")
	}
	if IsSerializationError(errors.New("syntax error near SELECT")) {
		t.Error("syntax errors must not be retryable")
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	tolerated := []error{
		errors.New("duplicate column name: extra"),
		errors.New(`pq: relation "idx_users" already exists`),
		errors.New("Error 1061: Duplicate key name 'idx_users'"),
		errors.New("There is already an object named 'idx_users' in the database."),
	}
	for _, err := range tolerated {
		if !IsAlreadyExistsError(err) {
			t.Errorf("expected tolerated: %v", err)
		}
	}

	if IsAlreadyExistsError(nil) {
		t.Error("nil must not be tolerated")
	}
	if IsAlreadyExistsError(errors.New("permission denied for table users")) {
		t.Error("permission errors must not be tolerated")
	}
}

func TestIsDuplicateRow(t *testing.T) {
	dupes := []error{
		errors.New("UNIQUE constraint failed: nodes.node_id"),
		errors.New("Error 1062: Duplicate entry 'node-a' for key 'PRIMARY'"),
		errors.New(`ERROR: duplicate key value violates unique constraint "nodes_pkey"`),
		errors.New("Violation of PRIMARY KEY constraint 'PK_nodes'"),
	}
	for _, err := range dupes {
		if !isDuplicateRow(err) {
			t.Errorf("expected duplicate: %v", err)
		}
	}
	if isDuplicateRow(nil) || isDuplicateRow(errors.New("connection refused")) {
		t.Error("non-duplicate errors misclassified")
	}
}

func TestIsConflict(t *testing.T) {
	if !IsConflict(ErrConflict) {
		t.Error("ErrConflict must be a conflict")
	}
	if IsConflict(errors.New("other")) {
		t.Error("unrelated error reported as a conflict")
	}
}
