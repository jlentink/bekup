package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/instancekeys"
)

// loadKeyring loads the master keyring (fail closed: bekup does not start
// without it), verifies it against the stored key-check values and checks
// that it opens the active instance signing key, if one exists, so a wrong
// or rotated-away keyring is caught at startup rather than at the first
// signature. The key itself is created lazily by the first signature
// (D-101).
func (a *App) loadKeyring(ctx context.Context) error {
	cfg := a.opts.Config
	raw, err := cfg.MasterKey()
	if err != nil {
		return fmt.Errorf("refusing to start: read master keyring: %w", err)
	}
	a.keyring, err = crypto.ParseKeyring(raw)
	crypto.Zero(raw)
	if err != nil {
		return fmt.Errorf("refusing to start: master keyring: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	// The key-check values catch a wrong keyring before anything is
	// decrypted (D-086, D-119).
	if err := a.checkKeyring(cctx); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	key, err := instancekeys.Load(cctx, sqlc.New(a.pool), a.keyring)
	switch {
	case errors.Is(err, instancekeys.ErrNoActiveKey):
	case err != nil:
		return fmt.Errorf("refusing to start: the master keyring cannot open the instance signing key in the database (wrong keyring?): %w", err)
	default:
		key.Destroy()
	}
	return nil
}

// newAuditHeadSigner loads the keyring (loadKeyring) and builds the
// periodic audit chain head signer.
func (a *App) newAuditHeadSigner(ctx context.Context) (*headSignerComponent, error) {
	if err := a.loadKeyring(ctx); err != nil {
		return nil, err
	}
	return &headSignerComponent{
		signer:   audit.NewHeadSigner(a.pool, a.keyring, a.auditor, nil),
		interval: a.opts.Config.Security.AuditHeadSignInterval.D(),
		logger:   a.opts.Logger,
	}, nil
}

// auditInsecureTransport records a startup over plain HTTP outside dev mode
// (D-095). Fail closed: serve does not start if the entry cannot be stored.
// The audit observer raises the security.system alert (once per day, with
// the periodic reminder of the security check; task 5.5).
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
// are audited). Fail closed like auditInsecureTransport. The audit
// observer raises the security.system alert (task 5.5).
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

// auditInsecureDBRoles records a startup in single-account mode
// (database.single_account, D-207): one account runs migrations and the
// app, so a compromised process can alter the schema and the audit log.
// Outside dev mode, fail closed like auditInsecureDBTLS; the audit
// observer raises the security.system alert (once a day, with the periodic
// reminder of the security check).
func (a *App) auditInsecureDBRoles(ctx context.Context) error {
	cfg := a.opts.Config
	if a.opts.Dev || !cfg.Database.SingleAccount {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	findings := a.dbLoginFindings
	if findings == nil {
		findings = []string{}
	}
	_, err := a.auditor.Record(cctx, audit.Event{
		Actor:      audit.SystemActor(),
		Action:     audit.ActionSecurityInsecureDBRoles,
		TargetType: "server",
		Details: audit.Details{
			"single_account":      true,
			"login_role_findings": findings,
			"process":             a.processName(),
		},
	})
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

// auditUnpinnedImage records a startup with the bekup image following a
// tag instead of a digest (BEKUP_IMAGE_MODE=following, or the runner
// reports an unpinned own image; D-212), once per process. Outside dev
// mode, fail closed like auditInsecureDBRoles; the audit observer raises
// the security.system alert (once a day, with the periodic reminder of
// the security check). A runner verdict that arrives after startup is
// audited by observeRunnerImage.
func (a *App) auditUnpinnedImage(ctx context.Context) error {
	a.image.mu.Lock()
	defer a.image.mu.Unlock()
	if a.opts.Dev || a.image.audited || !a.imageFollowing() {
		return nil
	}
	if err := a.recordUnpinnedImage(ctx); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

// recordUnpinnedImage records security.unpinned_image and marks it done;
// the caller holds a.image.mu.
func (a *App) recordUnpinnedImage(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(a.opts.Config))
	defer cancel()
	_, err := a.auditor.Record(cctx, audit.Event{
		Actor:      audit.SystemActor(),
		Action:     audit.ActionSecurityUnpinnedImage,
		TargetType: "server",
		Details: audit.Details{
			"image_mode":    deployguard.ImageModeFollowing,
			"source":        a.imageSource(),
			"version":       a.opts.Version,
			"setup_version": a.opts.Deployment.SetupVersion,
			"process":       a.processName(),
		},
	})
	if err != nil {
		return err
	}
	a.image.audited = true
	return nil
}

// recordServeAliasConflict records security.serve_alias_conflict (the
// audit observer raises the security alert): serve's network alias
// bekup-serve also resolves to another container on the proxy network
// (D-215). An error makes httpserver retry at the next resolution.
func (a *App) recordServeAliasConflict(ctx context.Context, foreign []netip.Addr) error {
	addrs := make([]string, 0, len(foreign))
	for _, f := range foreign {
		addrs = append(addrs, f.String())
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(a.opts.Config))
	defer cancel()
	_, err := a.auditor.Record(cctx, audit.Event{
		Actor:      audit.SystemActor(),
		Action:     audit.ActionSecurityServeAliasConflict,
		TargetType: "server",
		Details: audit.Details{
			"alias":     httpserver.ServeAlias,
			"addresses": addrs,
			"process":   a.processName(),
		},
	})
	if err != nil {
		return fmt.Errorf("record %s: %w", audit.ActionSecurityServeAliasConflict, err)
	}
	return nil
}

// processName is "serve" or "worker" (audit details).
func (a *App) processName() string {
	if a.opts.Mode == ModeWorker {
		return "worker"
	}
	return "serve"
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
