package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
)

func newAuditCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Verify the audit log hash chain and sign its head",
		Long: "Verify the tamper-evident audit log and manage signed chain heads.\n" +
			"Details: docs/security/audit.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newAuditVerifyCmd(g), newAuditSignHeadCmd(g))
	return cmd
}

func newAuditVerifyCmd(g *globalFlags) *cobra.Command {
	var (
		fromID   int64
		asJSON   bool
		headFile string
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Recompute the audit hash chain and check the signed chain head (non-zero exit on tampering)",
		Long: "Streams the audit log in id order, recomputes every hash, checks the prev_hash links and the\n" +
			"genesis rule, and checks the newest signed chain head (and --head-file, an exported head).\n" +
			"Reports the first broken link and exits non-zero when anything does not verify.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if fromID < 0 {
				return errors.New("--from-id must not be negative")
			}
			opts := audit.VerifyOptions{FromID: fromID}
			if headFile != "" {
				h, err := audit.ReadHeadFile(headFile)
				if err != nil {
					return err
				}
				opts.ExternalHead = &h
			}
			pool, _, cleanup, err := auditSetup(c, g, false)
			if err != nil {
				return err
			}
			defer cleanup()
			rep, err := audit.Verify(c.Context(), pool, opts)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else if err := writeVerifyReport(c.OutOrStdout(), rep); err != nil {
				return err
			}
			return rep.Err()
		},
	}
	f := cmd.Flags()
	f.Int64Var(&fromID, "from-id", 0, "start at this audit id (the link to the preceding row is still checked); default: the whole chain")
	f.BoolVar(&asJSON, "json", false, "print the report as JSON")
	f.StringVar(&headFile, "head-file", "", "also check an exported signed head (from sign-head --export-file or an export destination)")
	return cmd
}

func newAuditSignHeadCmd(g *globalFlags) *cobra.Command {
	var exportFile string
	cmd := &cobra.Command{
		Use:   "sign-head",
		Short: "Sign the current audit chain head with the instance key and store it",
		Long: "Signs the newest audit row's (id, hash) with the instance ed25519 key (created on first use),\n" +
			"stores it in audit_chain_heads and, with --export-file, writes it as JSON. Keep exported heads\n" +
			"off the database host: they make a rewrite of the whole database detectable.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			pool, kr, cleanup, err := auditSetup(c, g, true)
			if err != nil {
				return err
			}
			defer cleanup()
			var exp audit.HeadExporter = audit.NopExporter{}
			if strings.TrimSpace(exportFile) != "" {
				exp = audit.FileExporter{Path: exportFile}
			}
			a := newCLIAuditor(pool)
			h, err := audit.NewHeadSigner(pool, kr, a, exp).SignHead(c.Context())
			if err != nil && !errors.Is(err, audit.ErrExportFailed) {
				return err
			}
			if _, perr := fmt.Fprintf(c.OutOrStdout(), "signed audit chain head: id %d, hash %x, key %s, at %s\n",
				h.ChainID, []byte(h.ChainHash), h.KeyID, h.SignedAt.Format(time.RFC3339Nano)); perr != nil {
				return perr
			}
			return err
		},
	}
	cmd.Flags().StringVar(&exportFile, "export-file", "", "also write the signed head as JSON to this file")
	return cmd
}

// auditSetup loads the config, connects as the app role and, with
// needKeyring, loads the master keyring. cleanup closes everything.
func auditSetup(c *cobra.Command, g *globalFlags, needKeyring bool) (*pgxpool.Pool, *crypto.Keyring, func(), error) {
	pool, res, _, closePool, err := openAppDB(c, g, "bekup-audit")
	if err != nil {
		return nil, nil, nil, err
	}
	var kr *crypto.Keyring
	if needKeyring {
		raw, err := res.Config.MasterKey()
		if err != nil {
			closePool()
			return nil, nil, nil, fmt.Errorf("read master keyring: %w", err)
		}
		kr, err = crypto.ParseKeyring(raw)
		crypto.Zero(raw)
		if err != nil {
			closePool()
			return nil, nil, nil, fmt.Errorf("master keyring: %w", err)
		}
	}
	return pool, kr, func() {
		closePool()
		if kr != nil {
			kr.Destroy()
		}
	}, nil
}

// openAppDB loads the config, builds the redacting logger and connects to
// the app database as bekup_app with the configured TLS policy (D-100).
// cleanup closes the pool.
func openAppDB(c *cobra.Command, g *globalFlags, appName string) (*pgxpool.Pool, *config.Result[config.Bekup], *slog.Logger, func(), error) {
	res, err := config.LoadBekup(g.options(c))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger, err := newLogger(c.ErrOrStderr(), res)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	logLoaded(c, logger, res.Path, res.Warnings)
	opts, err := db.PoolOptionsFromConfig(res.Config, appName)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("database TLS: %w", err)
	}
	url, err := res.Config.DatabaseURL()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read database URL: %w", err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), res.Config.Database.ConnectTimeout.D())
	pool, err := db.NewPool(ctx, url, opts)
	cancel()
	config.Zero(url)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return pool, res, logger, func() {
		pool.Close()
		logger.DebugContext(context.WithoutCancel(c.Context()), "command finished", slog.String("command", c.CommandPath()))
	}, nil
}

func writeVerifyReport(w io.Writer, r *audit.Report) error {
	var b strings.Builder
	scope := "whole chain"
	if r.Partial {
		scope = fmt.Sprintf("from id %d (earlier rows not verified)", r.FromID)
	}
	fmt.Fprintf(&b, "audit log: %d rows verified, %s", r.Rows, scope)
	if r.Rows > 0 {
		fmt.Fprintf(&b, ", ids %d..%d, head hash %s", r.FirstID, r.LastID, r.LastHash)
	}
	b.WriteString("\n")
	if r.IDGaps > 0 {
		fmt.Fprintf(&b, "note: %d unused ids (rolled-back inserts; harmless while the links verify)\n", r.IDGaps)
	}
	if br := r.Broken; br != nil {
		fmt.Fprintf(&b, "BROKEN at id %d (%s): %s: %s\n", br.ID, br.TS.UTC().Format(time.RFC3339Nano), br.Kind, br.Reason)
	}
	if r.NoSignedHead {
		b.WriteString("warning: no signed chain head in the database (run `bekup audit sign-head`); truncation of the newest rows is not detectable\n")
	}
	for _, h := range r.Heads {
		status := "OK"
		if !h.OK {
			status = "FAILED: " + h.Problem
		}
		fmt.Fprintf(&b, "signed head (%s): id %d, key %s, signed %s: %s\n", h.Source, h.ChainID, h.KeyID,
			h.SignedAt.UTC().Format(time.RFC3339), status)
	}
	if r.OK {
		b.WriteString("result: OK\n")
	} else {
		b.WriteString("result: TAMPERING DETECTED\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
