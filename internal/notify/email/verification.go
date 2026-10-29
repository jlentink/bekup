package email

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Verification parameters.
const (
	// VerificationTTL is how long a verification link works.
	VerificationTTL = 24 * time.Hour
	// VerificationCooldown is the minimum time between two links for one
	// account (enforced in the database, across processes).
	VerificationCooldown = 60 * time.Second
	// VerificationPath is the web UI route of the link; the code travels
	// in the URL fragment, so it never reaches server or proxy logs nor a
	// Referer header.
	VerificationPath = "/verify-email"
	// maxVerificationFailures deletes a pending verification after this
	// many wrong codes.
	maxVerificationFailures = 5
	codeBytes               = 32
	macPurpose              = "email-verification"
)

// Errors of the verification.
var (
	// ErrAlreadyVerified: the account's address is already verified.
	ErrAlreadyVerified = errors.New("email: the address is already verified")
	// ErrInvalidCode: the code is wrong, used, expired, issued to another
	// account or for another address (one generic answer).
	ErrInvalidCode = errors.New("email: the verification link is invalid or has expired")
	// ErrCooldown: a link was sent less than VerificationCooldown ago.
	ErrCooldown = errors.New("email: a verification link was sent recently")
	// ErrNoAccount: the account does not exist (deleted) or is disabled.
	ErrNoAccount = errors.New("email: account not found")
)

// VerifierOptions configure NewVerifier.
type VerifierOptions struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	Mailer  Mailer
	// BaseURL is server.base_url; links are built from it only.
	BaseURL string
	// Limiter is the per-account limit of requests and verification
	// attempts; nil: 5 per 15 minutes (burst 3).
	Limiter func(key string) (bool, time.Duration)
	Logger  *slog.Logger
}

// Verifier implements the user e-mail verification (D-178).
type Verifier struct {
	o       VerifierOptions
	base    *url.URL
	limiter *serviceutil.Limiter
}

// NewVerifier validates o.
func NewVerifier(o VerifierOptions) (*Verifier, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Mailer == nil {
		return nil, errors.New("email: DB, Keyring, Auditor and Mailer are required")
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("email: BaseURL must be an absolute http(s) URL")
	}
	if o.Limiter == nil {
		rl, err := httpserver.NewRateLimiter[string](httpserver.RateLimitOptions{
			PerSecond: 5.0 / (15 * 60), Burst: 3, MaxClients: 10000,
		})
		if err != nil {
			return nil, err
		}
		o.Limiter = rl.Allow
	}
	lim, err := serviceutil.NewLimiter(8, o.Limiter)
	if err != nil {
		return nil, fmt.Errorf("email: %w", err)
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Verifier{o: o, base: u, limiter: lim}, nil
}

// VerificationRequest is the result of Request.
type VerificationRequest struct {
	SentTo    string
	ExpiresAt time.Time
}

// macInput binds a code to the account and the address.
func macInput(userID uuid.UUID, email string, code []byte) []byte {
	b := make([]byte, 0, 16+1+len(email)+1+len(code))
	b = append(b, userID[:]...)
	b = append(b, 0)
	b = append(b, strings.ToLower(email)...)
	b = append(b, 0)
	return append(b, code...)
}

// link returns the verification URL for code.
func (v *Verifier) link(code string) string {
	u := *v.base
	u.Path = strings.TrimSuffix(u.Path, "/") + VerificationPath
	u.RawQuery = ""
	u.Fragment = "code=" + code
	return u.String()
}

// Request sends a new verification link to the caller's current address
// (replacing an older link). Rate limited per account and by
// VerificationCooldown; delivery failures are a *SendError (the link is
// then voided).
func (v *Verifier) Request(ctx context.Context, actor Actor) (VerificationRequest, error) {
	release, err := v.limiter.Acquire("verify_request:" + actor.UserID.String())
	if err != nil {
		return VerificationRequest{}, err
	}
	defer release()
	q := sqlc.New(v.o.DB)
	st, err := q.GetEmailVerificationState(ctx, actor.UserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && st.Disabled) {
		return VerificationRequest{}, ErrNoAccount
	}
	if err != nil {
		return VerificationRequest{}, fmt.Errorf("email: load account: %w", err)
	}
	if st.EmailVerifiedAt != nil {
		return VerificationRequest{}, ErrAlreadyVerified
	}
	if err := CheckAddress(st.Email); err != nil {
		return VerificationRequest{}, fieldErr("email", "the account's e-mail address cannot receive mail: %s", err.Error())
	}
	code := make([]byte, codeBytes)
	if _, err := rand.Read(code); err != nil {
		return VerificationRequest{}, fmt.Errorf("email: code: %w", err)
	}
	defer crypto.Zero(code)
	keyID, mac, err := v.o.Keyring.MAC(macPurpose, macInput(actor.UserID, st.Email, code))
	if err != nil {
		return VerificationRequest{}, fmt.Errorf("email: mac: %w", err)
	}
	expires := time.Now().Add(VerificationTTL)
	err = db.Tx(ctx, v.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetEmailVerificationForUpdate(ctx, actor.UserID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("email: load verification: %w", err)
		case time.Since(cur.CreatedAt) < VerificationCooldown:
			return ErrCooldown
		default:
			if _, err := q.DeleteEmailVerification(ctx, actor.UserID); err != nil {
				return fmt.Errorf("email: replace verification: %w", err)
			}
		}
		err = q.InsertEmailVerification(ctx, sqlc.InsertEmailVerificationParams{
			UserID: actor.UserID, Email: st.Email, CodeMac: mac, MacKeyID: keyID[:], ExpiresAt: expires,
		})
		if serviceutil.IsUniqueViolation(err) {
			return ErrCooldown // a concurrent request won
		}
		if err != nil {
			return fmt.Errorf("email: store verification: %w", err)
		}
		_, err = v.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(actor.UserID), Action: audit.ActionUserEmailVerificationRequested,
			TargetType: "user", TargetID: actor.UserID.String(), IP: actor.IP,
			Details: audit.Details{"email": st.Email, "expires_at": expires.UTC().Format(time.RFC3339)},
		})
		return err
	})
	if err != nil {
		return VerificationRequest{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(code)
	c, err := Render(Notice{
		Subject: "Verify your e-mail address for bekup",
		Heading: "Verify your e-mail address",
		Paragraphs: []string{
			"Someone (hopefully you) asked bekup to send notifications to this address. Open the link below while signed in to bekup to confirm it.",
			"The link works once and expires in 24 hours. If you did not ask for this, ignore this message: nothing will be sent to this address.",
		},
		Action: &Action{Label: "Verify e-mail address", URL: v.link(encoded)},
	})
	if err == nil {
		err = v.o.Mailer.Send(ctx, NewMessage([]string{st.Email}, c))
	}
	if err != nil {
		// Void the link that was never delivered (also lifts the cooldown).
		if _, derr := sqlc.New(v.o.DB).DeleteEmailVerificationCode(context.WithoutCancel(ctx), sqlc.DeleteEmailVerificationCodeParams{
			UserID: actor.UserID, CodeMac: mac,
		}); derr != nil {
			v.o.Logger.WarnContext(ctx, "email_verification_void_failed", slog.Any("error", derr))
		}
		return VerificationRequest{}, err
	}
	return VerificationRequest{SentTo: st.Email, ExpiresAt: expires}, nil
}

// Verify redeems code for the caller's account: it must have been issued to
// this account for its current address, be unused and unexpired. Every
// failure is ErrInvalidCode; wrong codes count towards deleting the pending
// verification.
func (v *Verifier) Verify(ctx context.Context, actor Actor, code string) error {
	release, err := v.limiter.Acquire("verify_code:" + actor.UserID.String())
	if err != nil {
		return err
	}
	defer release()
	raw, derr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(code))
	if derr != nil || len(raw) != codeBytes {
		raw = nil
	}
	defer crypto.Zero(raw)
	var failure string
	err = db.Tx(ctx, v.o.DB, func(q *sqlc.Queries) error {
		failure = ""
		row, err := q.GetEmailVerificationForUpdate(ctx, actor.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			failure = "no_pending_verification"
			return nil
		}
		if err != nil {
			return fmt.Errorf("email: load verification: %w", err)
		}
		u, err := q.GetUserForUpdate(ctx, actor.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return fmt.Errorf("email: load account: %w", err)
		}
		ok := false
		if raw != nil && len(row.MacKeyID) == len(crypto.KeyID{}) {
			var kid crypto.KeyID
			copy(kid[:], row.MacKeyID)
			want, err := v.o.Keyring.MACWith(kid, macPurpose, macInput(actor.UserID, row.Email, raw))
			if err == nil {
				ok = hmac.Equal(want, row.CodeMac)
			}
		}
		switch {
		case !ok:
			failure = "wrong_code"
		case !strings.EqualFold(row.Email, u.Email):
			failure = "address_changed"
		case !time.Now().Before(row.ExpiresAt):
			failure = "expired"
		case u.Disabled:
			failure = "account_disabled"
		}
		if failure != "" {
			n, err := q.RecordEmailVerificationFailure(ctx, actor.UserID)
			if err != nil {
				return fmt.Errorf("email: record failure: %w", err)
			}
			if n >= maxVerificationFailures || failure == "expired" || failure == "address_changed" {
				if _, err := q.DeleteEmailVerification(ctx, actor.UserID); err != nil {
					return fmt.Errorf("email: delete verification: %w", err)
				}
			}
			_, err = v.o.Auditor.RecordTx(ctx, q, audit.Event{
				Actor: audit.UserActor(actor.UserID), Action: audit.ActionUserEmailVerificationFailed,
				TargetType: "user", TargetID: actor.UserID.String(), IP: actor.IP,
				Details: audit.Details{"reason": failure, "failed_attempts": n},
			})
			return err
		}
		if _, err := q.DeleteEmailVerification(ctx, actor.UserID); err != nil {
			return fmt.Errorf("email: delete verification: %w", err)
		}
		n, err := q.MarkEmailVerified(ctx, sqlc.MarkEmailVerifiedParams{ID: actor.UserID, Email: row.Email})
		if err != nil {
			return fmt.Errorf("email: mark verified: %w", err)
		}
		if n != 1 {
			return ErrInvalidCode
		}
		_, err = v.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(actor.UserID), Action: audit.ActionUserEmailVerified,
			TargetType: "user", TargetID: actor.UserID.String(), IP: actor.IP,
			Details: audit.Details{"email": row.Email},
		})
		return err
	})
	if err != nil {
		return err
	}
	if failure != "" {
		if failure == "no_pending_verification" {
			// Audited outside the (read-only) transaction.
			if _, err := v.o.Auditor.Record(ctx, audit.Event{
				Actor: audit.UserActor(actor.UserID), Action: audit.ActionUserEmailVerificationFailed,
				TargetType: "user", TargetID: actor.UserID.String(), IP: actor.IP,
				Details: audit.Details{"reason": failure},
			}); err != nil {
				return err
			}
		}
		return ErrInvalidCode
	}
	return nil
}

// Recipient is a user's address for delivery.
type Recipient struct {
	Email    string
	Verified bool
}

// IsVerified reports whether the user (not deleted, not disabled) has a
// verified address and returns it. Task 5.3 delivers e-mail to a user only
// when Verified is true. An address change clears the verification in the
// database (users trigger, D-178).
func (v *Verifier) IsVerified(ctx context.Context, userID uuid.UUID) (Recipient, error) {
	return IsVerified(ctx, v.o.DB, userID)
}

// IsVerified is Verifier.IsVerified for callers without a Verifier (the
// notify workers).
func IsVerified(ctx context.Context, d sqlc.DBTX, userID uuid.UUID) (Recipient, error) {
	st, err := sqlc.New(d).GetEmailVerificationState(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, nil
	}
	if err != nil {
		return Recipient{}, fmt.Errorf("email: load account: %w", err)
	}
	return Recipient{Email: st.Email, Verified: st.EmailVerifiedAt != nil && !st.Disabled}, nil
}
