// Package notifysvc is the composition seam between src/notify and the rest
// of the server: it builds a *notify.Notifier from the loaded configuration,
// runs the PART 18 SMTP auto-detect sequence, and registers the package's
// background tasks with the built-in scheduler. Every other package that
// needs to send a notification takes a *notify.Notifier directly (or a
// small interface over it) rather than importing this package.
package notifysvc

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/scheduler"
)

// retrySchedule and cleanupSchedule are the cadences for the two tasks this
// package registers. Neither is a PART 19 built-in task, so they are added
// fresh via Scheduler.Register rather than bound to a pre-declared name.
const (
	retrySchedule   = "@every 5m"
	cleanupSchedule = "0 3 * * *"
)

// New builds a *notify.Notifier from the loaded server configuration and an
// already-open database handle. It does not run SMTP auto-detection or
// register scheduler tasks — call DetectSMTP and RegisterScheduler after.
func New(cfg *config.Config, db *database.DB) (*notify.Notifier, error) {
	email := cfg.Server.Notifications.Email
	opts := notify.Options{
		DB:        db,
		ConfigDir: config.ConfigDir(),
		AppName:   cfg.Server.Branding.Title,
		AppURL:    cfg.Server.BaseURL,
		FQDN:      cfg.Server.FQDN,
		AdminEmail: cfg.Server.Contact.Admin.Email,
		Contacts: func() *config.ContactConfig {
			return &cfg.Server.Contact
		},
		SMTP: notify.SMTPSettings{
			Host:      email.SMTP.Host,
			Port:      email.SMTP.Port,
			Username:  email.SMTP.Username,
			Password:  email.SMTP.Password,
			TLS:       email.SMTP.TLS,
			FromName:  email.From.Name,
			FromEmail: email.From.Email,
		},
	}
	return notify.New(opts)
}

// DetectSMTP runs the PART 18 auto-detect sequence (loopback, docker
// gateway, default gateway, FQDN, global IPv4, mail./smtp. subdomains) when
// no SMTP host is already configured. It reports whether a relay was found
// and activated; a false result is a normal outcome, not an error. When
// detection genuinely finds nothing (as opposed to SMTP already being
// configured, which also returns false), it dispatches smtp_not_configured
// per AI.md PART 18's decision matrix.
func DetectSMTP(ctx context.Context, n *notify.Notifier) bool {
	alreadyConfigured := n.SMTP().Settings().Host != ""
	found := n.DetectSMTP(ctx, defaultGatewayIP(), config.GlobalIP())
	if !found && !alreadyConfigured {
		if err := n.Notify(ctx, notify.Message{Event: notify.EventSMTPNotConfigured}); err != nil {
			logging.L().Warn("smtp_not_configured notification failed", "error", err.Error())
		}
	}
	return found
}

// defaultGatewayIP returns this host's default IPv4 gateway, read from
// /proc/net/route. It returns "" on any failure or on a non-Linux host,
// which simply skips that step of the auto-detect sequence.
func defaultGatewayIP() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan() // header line
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		// Destination "00000000" is the default route.
		if fields[1] != "00000000" {
			continue
		}
		gateway, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			return ""
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, uint32(gateway))
		return ip.String()
	}
	return ""
}

// RegisterScheduler adds the notify package's two background tasks
// (retry queue drain, stored-notification cleanup) to sched. Neither task
// is a PART 19 built-in, so they are added with Register rather than Bind.
func RegisterScheduler(sched *scheduler.Scheduler, n *notify.Notifier) error {
	if err := sched.Register(scheduler.Task{
		Name:        notify.TaskRetry,
		Schedule:    retrySchedule,
		Title:       "Notification retry",
		Description: "Drains the notification delivery retry queue.",
		Run:         n.Retry,
	}); err != nil {
		return err
	}
	return sched.Register(scheduler.Task{
		Name:        notify.TaskCleanup,
		Schedule:    cleanupSchedule,
		Title:       "Notification cleanup",
		Description: "Prunes stored notifications and expired dedup claims.",
		Run:         n.Cleanup,
	})
}

// NotifyStartup dispatches the startup lifecycle event. Per PART 18's
// decision matrix this event is WebUI/webhook only, never email, and the
// catalog entry carries no personal content, so it is dispatched with no
// recipients — only the webhook fan-out fires until a composition root
// supplies the admin recipient list for the in-app notification center.
func NotifyStartup(ctx context.Context, n *notify.Notifier) error {
	return n.Notify(ctx, notify.Message{Event: notify.EventStartup})
}

// NotifyShutdown dispatches the shutdown lifecycle event. See NotifyStartup
// for why it is sent without recipients.
func NotifyShutdown(ctx context.Context, n *notify.Notifier) error {
	return n.Notify(ctx, notify.Message{Event: notify.EventShutdown})
}
