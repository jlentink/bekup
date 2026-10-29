package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
)

// Break-glass account administration on the host (task 1.6, D-069). The
// commands connect as bekup_app with the normal config and record every
// action in the audit log with actor kind cli, the OS user and the
// hostname.

func newAdminCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable",
		Long: "Manage accounts from the host when nobody can sign in (D-069). Every action is recorded in the\n" +
			"audit log with actor kind cli, the OS user and the hostname. Passwords are read from a terminal\n" +
			"prompt (twice) or from --password-file, never from a flag value. Details: docs/operations.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newAdminCreateCmd(g), newAdminResetPasswordCmd(g), newAdminReset2FACmd(g), newAdminUnlockCmd(g), newAdminDisableCmd(g))
	return cmd
}

// adminEnv is a connected user admin service for one command.
type adminEnv struct {
	users   *auth.UserAdmin
	cleanup func()
}

func openAdmin(c *cobra.Command, g *globalFlags) (*adminEnv, error) {
	pool, res, logger, closePool, err := openAppDB(c, g, "bekup-admin")
	if err != nil {
		return nil, err
	}
	sec := res.Config.Security
	hasher, err := app.NewHasher(&sec)
	if err != nil {
		closePool()
		return nil, err
	}
	sessions := auth.NewSessions(pool, auth.SessionOptions{Logger: logger})
	users, err := auth.NewUserAdmin(auth.UserAdminOptions{
		DB: pool, Hasher: hasher, Sessions: sessions, Logger: logger,
		Policy:  auth.NewPasswordPolicy(sec.PasswordMinLength, sec.PasswordMinScore),
		Auditor: newCLIAuditor(pool),
	})
	if err != nil {
		closePool()
		return nil, err
	}
	return &adminEnv{users: users, cleanup: closePool}, nil
}

func addPasswordFileFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVar(p, "password-file", "",
		"read the new password from this file (owner-only permissions; surrounding whitespace is trimmed) instead of prompting")
}

func newAdminCreateCmd(g *globalFlags) *cobra.Command {
	var email, displayName, role, passwordFile string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an account (by default an admin) with a password you enter",
		Long: "Creates an account with the given e-mail address, display name and role (default admin). The\n" +
			"password policy applies. This is the only way to create the first admin (D-113): run it once\n" +
			"after installing, then sign in to the web UI.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			r := auth.Role(strings.ToLower(strings.TrimSpace(role)))
			if !r.Valid() {
				return fmt.Errorf("--role must be admin, maintainer or user (got %q)", role)
			}
			password, err := readNewPassword(c, passwordFile)
			if err != nil {
				return err
			}
			env, err := openAdmin(c, g)
			if err != nil {
				return err
			}
			defer env.cleanup()
			u, _, err := env.users.Create(c.Context(), auth.OperatorActor(), auth.NewUser{
				Email: email, DisplayName: displayName, Role: r, Password: password,
			})
			if err != nil {
				return cliUserError(err)
			}
			return printf(c, "created %s account %s (id %s); the action was recorded in the audit log\n", u.Role, u.Email, u.ID)
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "e-mail address (sign-in name) of the account (required)")
	f.StringVar(&displayName, "display-name", "", "display name of the account (required)")
	f.StringVar(&role, "role", string(auth.RoleAdmin), "role: admin, maintainer or user")
	addPasswordFileFlag(cmd, &passwordFile)
	mustRequire(cmd, "email", "display-name")
	return cmd
}

func newAdminResetPasswordCmd(g *globalFlags) *cobra.Command {
	var email, passwordFile string
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Set a new password for an account, clear its lockout and revoke all its sessions",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			password, err := readNewPassword(c, passwordFile)
			if err != nil {
				return err
			}
			env, err := openAdmin(c, g)
			if err != nil {
				return err
			}
			defer env.cleanup()
			u, err := env.users.ByEmail(c.Context(), email)
			if err != nil {
				return cliUserError(err)
			}
			_, revoked, err := env.users.ResetPassword(c.Context(), auth.OperatorActor(), u.ID, password)
			if err != nil {
				return cliUserError(err)
			}
			return printf(c, "password of %s (id %s) reset, lockout cleared, %d session(s) revoked; the action was recorded in the audit log\n",
				u.Email, u.ID, revoked)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "e-mail address of the account (required)")
	addPasswordFileFlag(cmd, &passwordFile)
	mustRequire(cmd, "email")
	return cmd
}

func newAdminReset2FACmd(g *globalFlags) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "reset-2fa",
		Short: "Remove every second factor of an account (lost authenticator) and revoke all its sessions",
		Long: "Lost-factor recovery (D-112): removes the account's TOTP authenticator, passkeys and recovery\n" +
			"codes, clears its second-factor lockout and revokes all its sessions. The account then signs in\n" +
			"with its password alone; when the 2FA policy requires a factor for its role, a new enrollment\n" +
			"grace period starts now. Only use it after verifying the person's identity out of band.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			env, err := openAdmin(c, g)
			if err != nil {
				return err
			}
			defer env.cleanup()
			u, err := env.users.ByEmail(c.Context(), email)
			if err != nil {
				return cliUserError(err)
			}
			if _, err := fmt.Fprintf(c.ErrOrStderr(), "warning: removing every second factor of %s; until it enrolls a new one, "+
				"its password alone signs it in\n", u.Email); err != nil {
				return err
			}
			res, err := env.users.ResetSecondFactors(c.Context(), auth.OperatorActor(), u.ID)
			if err != nil {
				return cliUserError(err)
			}
			return printf(c, "second factors of %s (id %s) reset: TOTP removed %t, %d passkey(s), %d recovery code(s) removed, "+
				"%d session(s) revoked; the action was recorded in the audit log\n",
				u.Email, u.ID, res.HadTOTP, res.Passkeys, res.RecoveryCodes, res.RevokedSessions)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "e-mail address of the account (required)")
	mustRequire(cmd, "email")
	return cmd
}

func newAdminUnlockCmd(g *globalFlags) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Clear the login lockout of an account",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			env, err := openAdmin(c, g)
			if err != nil {
				return err
			}
			defer env.cleanup()
			u, err := env.users.ByEmail(c.Context(), email)
			if err != nil {
				return cliUserError(err)
			}
			if _, err := env.users.Unlock(c.Context(), auth.OperatorActor(), u.ID); err != nil {
				return cliUserError(err)
			}
			return printf(c, "lockout of %s (id %s) cleared; the action was recorded in the audit log\n", u.Email, u.ID)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "e-mail address of the account (required)")
	mustRequire(cmd, "email")
	return cmd
}

func newAdminDisableCmd(g *globalFlags) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable an account and revoke all its sessions",
		Long: "Disables the account (it can no longer sign in) and revokes all its sessions. The last active\n" +
			"admin cannot be disabled: create another admin first.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			env, err := openAdmin(c, g)
			if err != nil {
				return err
			}
			defer env.cleanup()
			u, err := env.users.ByEmail(c.Context(), email)
			if err != nil {
				return cliUserError(err)
			}
			res, err := env.users.SetDisabled(c.Context(), auth.OperatorActor(), u.ID, true)
			if err != nil {
				return cliUserError(err)
			}
			return printf(c, "account %s (id %s) disabled, %d session(s) revoked; the action was recorded in the audit log\n",
				u.Email, u.ID, res.RevokedSessions)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "e-mail address of the account (required)")
	mustRequire(cmd, "email")
	return cmd
}

func mustRequire(cmd *cobra.Command, flags ...string) {
	for _, f := range flags {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(err)
		}
	}
}

func printf(c *cobra.Command, format string, args ...any) error {
	_, err := fmt.Fprintf(c.OutOrStdout(), format, args...)
	return err
}

// cliUserError turns service errors into operator-facing messages.
func cliUserError(err error) error {
	var (
		fe *auth.FieldError
		pe *auth.PolicyError
	)
	switch {
	case errors.As(err, &fe):
		return fmt.Errorf("invalid --%s: %s", flagName(fe.Field), fe.Message)
	case errors.As(err, &pe):
		return fmt.Errorf("password rejected: %s", pe.Message)
	case errors.Is(err, auth.ErrUserNotFound):
		return errors.New("no account with this e-mail address")
	case errors.Is(err, auth.ErrEmailTaken):
		return errors.New("another account already uses this e-mail address")
	case errors.Is(err, auth.ErrLastAdmin):
		return errors.New("refusing: this is the last active admin (create another admin first)")
	default:
		return err
	}
}

func flagName(field string) string {
	switch field {
	case "displayName":
		return "display-name"
	default:
		return field
	}
}

// readNewPassword reads a new password from file (secret-file rules) or,
// without one, prompts twice on the terminal. It refuses to read a
// password from a non-terminal stdin, so passwords never come from argv,
// environment variables or pipes by accident.
func readNewPassword(c *cobra.Command, file string) (string, error) {
	if strings.TrimSpace(file) != "" {
		b, err := config.ReadSecretFile(file)
		if err != nil {
			return "", fmt.Errorf("--password-file: %w", err)
		}
		defer config.Zero(b)
		return string(b), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal to prompt for the password: use --password-file")
	}
	first, err := promptPassword(c.ErrOrStderr(), fd, "New password: ")
	if err != nil {
		return "", err
	}
	defer config.Zero(first)
	second, err := promptPassword(c.ErrOrStderr(), fd, "Repeat the new password: ")
	if err != nil {
		return "", err
	}
	defer config.Zero(second)
	if len(first) == 0 {
		return "", errors.New("empty password")
	}
	if subtle.ConstantTimeCompare(first, second) != 1 {
		return "", errors.New("the passwords do not match")
	}
	return string(first), nil
}

func promptPassword(w io.Writer, fd int, prompt string) ([]byte, error) {
	if _, err := io.WriteString(w, prompt); err != nil {
		return nil, err
	}
	b, err := term.ReadPassword(fd)
	_, werr := io.WriteString(w, "\n")
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}
	return b, werr
}
