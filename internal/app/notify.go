package app

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/storage"
)

// newNotify builds the notification core (task 5.3, D-183) for serve and
// the workers: the e-mail sender (5.1) and the webhook sender (5.2) behind
// the SSRF guard, links from server.base_url, the noise rules of
// [notifications] and the policy for the delivery-time access checks. It
// needs the keyring and the policy and comes before the queue (its
// workers are registered with the others).
func (a *App) newNotify() error {
	cfg := a.opts.Config
	guard := a.guards.Guard(netguard.ScopeSMTP)
	settings, err := email.NewSettings(email.SettingsOptions{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Guard: guard, Dev: a.opts.Dev, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	helo := ""
	if u, err := url.Parse(cfg.Server.BaseURL); err == nil {
		helo = u.Hostname()
	}
	mailer, err := email.NewSender(email.SenderOptions{
		Settings: settings, Guard: guard, Dev: a.opts.Dev, HeloName: helo, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	version := a.opts.Version
	if version == "" {
		version = "dev"
	}
	// Shared and personal webhooks dial through separate guards (D-198):
	// admin allowlist entries reach personal webhooks only when scoped to
	// them.
	sharedGuard := a.guards.Guard(netguard.ScopeSharedWebhooks)
	personalGuard := a.guards.Guard(netguard.ScopePersonalWebhooks)
	hooks, err := webhook.NewSender(sharedGuard, webhook.SenderOptions{Version: version, AllowHTTP: a.opts.Dev, Logger: a.opts.Logger})
	if err != nil {
		return err
	}
	personalHooks, err := webhook.NewSender(personalGuard, webhook.SenderOptions{Version: version, AllowHTTP: a.opts.Dev, Logger: a.opts.Logger})
	if err != nil {
		return err
	}
	policy := webhook.DefaultFailurePolicy()
	policy.MaxPermanent = cfg.Notifications.WebhookFailureThreshold
	svc, err := notify.New(notify.Options{
		DB: a.pool, Policy: a.policy, Keyring: a.keyring, Mailer: mailer, Webhooks: hooks, PersonalWebhooks: personalHooks,
		Queue: func() notify.Inserter {
			if a.queue == nil {
				return nil
			}
			return a.queue
		},
		BaseURL:           cfg.Server.BaseURL,
		CollapseWindow:    cfg.Notifications.CollapseWindow.D(),
		MaxPerUserPerHour: cfg.Notifications.MaxPerUserPerHour,
		FailurePolicy:     policy,
		// New inbox rows reach the accountEvents streams (task
		// 5.3b): NOTIFY to every serve instance, plus the local bus here.
		Publisher: a.publisher(),
		Logger:    a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.notifier = svc
	if a.opts.Mode == ModeWorker {
		return nil
	}
	// The notifications API of serve (task 5.3b): channels, preferences
	// and the inbox behind the GraphQL layer.
	a.notifyAPI, err = notify.NewAPI(notify.APIOptions{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Policy: a.policy,
		Guard: sharedGuard, PersonalGuard: personalGuard, Webhooks: hooks, PersonalWebhooks: personalHooks,
		Mailer: mailer, AllowHTTP: a.opts.Dev, Logger: a.opts.Logger,
	})
	return err
}

// emitNow emits a post-commit event; a failure is logged (the hooks that
// call it cannot fail their operation, and the audit log or the run
// tables keep the durable record).
func (a *App) emitNow(ctx context.Context, ev notify.Event) {
	if a.notifier == nil {
		return
	}
	if err := a.notifier.EmitNow(context.WithoutCancel(ctx), ev); err != nil {
		a.opts.Logger.WarnContext(ctx, "notification event could not be emitted",
			slog.String("event", string(ev.Type)), slog.Any("error", err))
	}
}

// restoreNotifier turns the restore and test-restore emit hooks (tasks
// 4.2, 4.6) into D-161 events.
type restoreNotifier struct{ a *App }

// Emit implements restore.Notifier.
func (n restoreNotifier) Emit(ctx context.Context, r restore.Notification) {
	d := notify.Detail{}
	if r.Status != "" {
		d["status"] = string(r.Status)
	}
	if r.ErrorCode != "" {
		d["error_code"] = r.ErrorCode
	}
	n.a.emitNow(ctx, notify.Event{
		Type: notify.EventType(r.Event), Subject: notify.Subject{JobID: r.JobID, RestoreID: r.RestoreID}, Detail: d,
	})
}

// EmitTestRestore implements restore.TestNotifier.
func (n restoreNotifier) EmitTestRestore(ctx context.Context, r restore.TestNotification) {
	d := notify.Detail{"status": string(r.Status)}
	if r.ErrorCode != "" {
		d["error_code"] = r.ErrorCode
	}
	s := notify.Subject{JobID: r.JobID, TestRestoreID: r.TestRestoreID}
	if r.RunID != nil {
		s.RunID = *r.RunID
	}
	n.a.emitNow(ctx, notify.Event{Type: notify.EventType(r.Event), Subject: s, Detail: d})
}

// retentionAlerts emits artifact.missing for an object that vanished
// without bekup deleting it (on top of the WARN log) to the job's
// audience. The security.system alerts of unexpected deletions and
// aborted sweeps come from their audit entries (notify.ObserveAudit).
func (a *App) retentionAlerts() retention.AlertFunc {
	log := retention.LogAlert(a.opts.Logger)
	return func(ctx context.Context, al retention.Alert) {
		log(ctx, al)
		if al.Kind != retention.AlertArtifactMissing {
			return
		}
		a.emitNow(ctx, notify.Event{
			Type: notify.ArtifactMissing,
			Subject: notify.Subject{
				JobID: al.JobID, RunID: al.RunID, ArtifactID: al.ArtifactID, DestinationID: al.DestinationID,
			},
		})
	}
}

// destinationTestFailed emits destination.test_failed.
func (a *App) destinationTestFailed(ctx context.Context, id uuid.UUID, code storage.ErrorCode) {
	a.emitNow(ctx, notify.Event{
		Type: notify.DestinationTestFailed, Subject: notify.Subject{DestinationID: id},
		Detail: notify.Detail{"error_code": string(code)},
	})
}
