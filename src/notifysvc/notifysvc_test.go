package notifysvc

import (
	"context"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/scheduler"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Server.Branding.Title = "cashp"
	cfg.Server.BaseURL = "https://example.test"
	cfg.Server.FQDN = "example.test"
	cfg.Server.Contact.Admin.Email = "admin@example.test"
	return cfg
}

func TestNew(t *testing.T) {
	db := testDB(t)
	n, err := New(testConfig(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if n == nil {
		t.Fatal("New returned a nil notifier")
	}
	names := n.TaskNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 task names, got %d: %v", len(names), names)
	}
}

func TestNewMissingContactStillBuilds(t *testing.T) {
	db := testDB(t)
	cfg := &config.Config{}
	if _, err := New(cfg, db); err != nil {
		t.Fatalf("New with zero-value config: %v", err)
	}
}

func TestDefaultGatewayIPNeverPanics(t *testing.T) {
	// Whatever the host returns (a real gateway, or "" when unavailable in
	// this sandbox), the call must never panic or hang.
	_ = defaultGatewayIP()
}

func TestDetectSMTPDoesNotBlock(t *testing.T) {
	db := testDB(t)
	n, err := New(testConfig(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Whether a relay answers depends on the sandbox's network (some CI
	// containers expose something on the docker gateway); the contract this
	// test checks is that detection completes within the timeout and never
	// panics, not a specific true/false outcome.
	done := make(chan struct{})
	go func() {
		DetectSMTP(ctx, n)
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("DetectSMTP did not return before the context deadline")
	}
}

// TestDetectSMTPNotifiesWhenNothingFound covers the "genuinely undetected"
// branch of DetectSMTP: with no SMTP host configured and no relay reachable
// in this sandbox, it must dispatch smtp_not_configured.
func TestDetectSMTPNotifiesWhenNothingFound(t *testing.T) {
	db := testDB(t)
	n, err := New(testConfig(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if found := DetectSMTP(ctx, n); found {
		t.Skip("a relay answered on this sandbox's network; the not-found branch cannot be exercised")
	}

	held, err := n.Store().DedupHeld(ctx, notify.EventSMTPNotConfigured+":")
	if err != nil {
		t.Fatalf("DedupHeld: %v", err)
	}
	if !held {
		t.Fatal("expected smtp_not_configured to have been dispatched")
	}
}

// TestDetectSMTPSkipsNotificationWhenAlreadyConfigured covers the
// already-configured branch: DetectSMTP must return false without
// dispatching smtp_not_configured when a host is already set.
func TestDetectSMTPSkipsNotificationWhenAlreadyConfigured(t *testing.T) {
	db := testDB(t)
	cfg := testConfig()
	cfg.Server.Notifications.Email.SMTP.Host = "smtp.example.test"
	n, err := New(cfg, db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	DetectSMTP(ctx, n)

	held, err := n.Store().DedupHeld(ctx, notify.EventSMTPNotConfigured+":")
	if err != nil {
		t.Fatalf("DedupHeld: %v", err)
	}
	if held {
		t.Fatal("smtp_not_configured must not dispatch when a host is already configured")
	}
}

func TestRegisterScheduler(t *testing.T) {
	db := testDB(t)
	n, err := New(testConfig(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sched := scheduler.New(scheduler.Options{StateDir: t.TempDir(), LogDir: t.TempDir()})
	if err := RegisterScheduler(sched, n); err != nil {
		t.Fatalf("RegisterScheduler: %v", err)
	}
	found := map[string]bool{}
	for _, status := range sched.Status() {
		found[status.Name] = true
	}
	if !found["notification_retry"] || !found["notification_cleanup"] {
		t.Fatalf("expected both notify tasks registered, got %v", found)
	}
}

func TestNotifyStartupAndShutdown(t *testing.T) {
	db := testDB(t)
	n, err := New(testConfig(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := NotifyStartup(ctx, n); err != nil {
		t.Fatalf("NotifyStartup: %v", err)
	}
	if err := NotifyShutdown(ctx, n); err != nil {
		t.Fatalf("NotifyShutdown: %v", err)
	}
}
