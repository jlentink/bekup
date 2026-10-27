package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
)

// newAuditHeadSigner loads the master keyring (fail closed: serve does not
// start without it) and checks that it opens the active instance signing
// key, if one exists, so a wrong or rotated-away keyring is caught at startup
// rather than at the first signature. The key itself is created lazily by
// the first signature (D-101).
func (a *App) newAuditHeadSigner(ctx context.Context) (*headSignerComponent, error) {
	cfg := a.opts.Config
	raw, err := cfg.MasterKey()
	if err != nil {
		return nil, fmt.Errorf("refusing to start: read master keyring: %w", err)
	}
	a.keyring, err = crypto.ParseKeyring(raw)
	crypto.Zero(raw)
	if err != nil {
		return nil, fmt.Errorf("refusing to start: master keyring: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	// The key-check values catch a wrong keyring before anything is
	// decrypted (D-086, D-119).
	if err := a.checkKeyring(cctx); err != nil {
		return nil, fmt.Errorf("refusing to start: %w", err)
	}
	key, err := instancekeys.Load(cctx, sqlc.New(a.pool), a.keyring)
	switch {
	case errors.Is(err, instancekeys.ErrNoActiveKey):
	case err != nil:
		return nil, fmt.Errorf("refusing to start: the master keyring cannot open the instance signing key in the database (wrong keyring?): %w", err)
	default:
		key.Destroy()
	}
	return &headSignerComponent{
		signer:   audit.NewHeadSigner(a.pool, a.keyring, a.auditor, nil),
		interval: cfg.Security.AuditHeadSignInterval.D(),
		logger:   a.opts.Logger,
	}, nil
}

// auditInsecureTransport records a startup over plain HTTP outside dev mode
// (D-095). Fail closed: serve does not start if the entry cannot be stored.
// The admin alert follows in task 5.5.
func (a *App) auditInsecureTransport(ctx context.Context) error {
	cfg := a.opts.Config
	if a.opts.Dev || !cfg.InsecureTransport() {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	_, err := a.auditor.Record(cctx, audit.Event{
		Actor:      audit.SystemActor(),
		Action:     audit.ActionSystemInsecureTransportStarted,
		TargetType: "server",
		Details: audit.Details{
			"base_url":            cfg.Server.BaseURL,
			"allow_insecure_http": cfg.Server.AllowInsecureHTTP,
		},
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

// auditInsecureDBTLS records a startup with a weakened database.tls_mode
// (require or disable) outside dev mode (D-001, D-100: weakened settings
// are audited). Fail closed like auditInsecureTransport. The admin alert
// follows in task 5.5.
func (a *App) auditInsecureDBTLS(ctx context.Context) error {
	cfg := a.opts.Config
	if a.opts.Dev || !cfg.Database.TLSWeakened() {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	_, err := a.auditor.Record(cctx, audit.Event{
		Actor:      audit.SystemActor(),
		Action:     audit.ActionSecurityInsecureDBTLS,
		TargetType: "server",
		Details: audit.Details{
			"tls_mode":       cfg.Database.TLSMode,
			"ca_file_set":    cfg.Database.TLSCAFile != "",
			"host_verified":  false,
			"chain_verified": cfg.Database.TLSMode == config.DBTLSRequire && cfg.Database.TLSCAFile != "",
		},
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

// headSignerComponent periodically signs the audit chain head (task 1.3).
// Signing failures are logged by the signer and never stop serve.
type headSignerComponent struct {
	signer   *audit.HeadSigner
	interval time.Duration
	logger   *slog.Logger
}

func (h *headSignerComponent) Name() string { return "audit head signer" }

func (h *headSignerComponent) Run(ctx context.Context) error {
	h.logger.InfoContext(ctx, "audit chain head signer started", slog.Duration("interval", h.interval))
	h.signer.Run(ctx, h.interval, h.logger)
	return nil
}
