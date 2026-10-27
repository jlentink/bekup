package app

import (
	"context"
	"fmt"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
)

// NewHasher builds the argon2id password hasher from [security] (serve and
// `bekup admin`).
func NewHasher(sec *config.Security) (*auth.Hasher, error) {
	return auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{
			MemoryKiB:   uint32(sec.Argon2MemoryKiB),  //nolint:gosec // G115: bounded by config validation (64 MiB..4 GiB)
			Iterations:  uint32(sec.Argon2Iterations), //nolint:gosec // G115: bounded by config validation (3..64)
			Parallelism: uint8(sec.Argon2Parallelism), //nolint:gosec // G115: bounded by config validation (1..255)
		},
		MaxConcurrent: sec.Argon2MaxConcurrent,
	})
}

// firstAdminCommand is the command an operator runs to create the first
// admin (D-113). The password comes from a terminal prompt or
// --password-file, never from a flag.
const firstAdminCommand = "bekup admin create --email <address> --display-name <name> [--password-file <path>]"

// newUserAdmin builds the user administration service (task 1.6). The
// first admin is created only with `bekup admin create` (D-113): when no
// active admin exists, serve logs the command to run at WARN and keeps
// running (nobody can sign in until an admin exists).
func (a *App) newUserAdmin(ctx context.Context) error {
	sec := a.opts.Config.Security
	policy := auth.NewPasswordPolicy(sec.PasswordMinLength, sec.PasswordMinScore)
	users, err := auth.NewUserAdmin(auth.UserAdminOptions{
		DB: a.pool, Hasher: a.auth.Hasher(), Policy: policy, Sessions: a.auth.Sessions(),
		Auditor: a.auditor, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(a.opts.Config))
	defer cancel()
	exists, err := users.ActiveAdminExists(cctx)
	if err != nil {
		return fmt.Errorf("check for an admin account: %w", err)
	}
	if !exists {
		a.opts.Logger.WarnContext(ctx,
			"NO ADMIN ACCOUNT: no active admin exists, so nobody can sign in. Create the first admin on the host with "+
				"`bekup admin create` (the password is read from a terminal prompt or --password-file; with compose: "+
				"docker compose run --rm admin create ...), then sign in to the web UI",
			"command", firstAdminCommand)
	}
	a.users = users
	return nil
}
