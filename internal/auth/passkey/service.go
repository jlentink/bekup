package passkey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Limits and ceremony parameters (D-109).
const (
	// CeremonyTimeout is the WebAuthn timeout sent to the browser and the
	// server-side lifetime of a pending challenge.
	CeremonyTimeout = 3 * time.Minute
	// MaxPasskeys is the most passkeys one user can register.
	MaxPasskeys = 20
	// MaxNameLength is the longest passkey name (characters).
	MaxNameLength = 64
	// MaxResponseBytes bounds a browser response (attestation or assertion
	// JSON).
	MaxResponseBytes = 32 << 10
	// UserVerification is requested for registration and assertion:
	// "preferred" (a passkey is the possession factor next to the
	// password; PIN-less security keys stay usable). Whether the user was
	// verified is recorded per passkey.
	UserVerification = protocol.VerificationPreferred
	// RPDisplayName is the relying party name shown by authenticators.
	RPDisplayName = "bekup"
	// defaultStepUpWindow is the step-up freshness when none is configured
	// (authz.MaxStepUpWindow).
	defaultStepUpWindow = 5 * time.Minute
)

// Ceremony kinds (webauthn_ceremonies.kind).
const (
	kindRegistration = "registration"
	kindAssertion    = "assertion"
)

// Errors of the service. Resolvers map them to API error codes; the
// underlying library error is logged, never shown.
var (
	// ErrUnavailable: passkeys cannot be used with this server.base_url
	// (Status().Reason says why).
	ErrUnavailable = errors.New("passkey: passkeys are unavailable on this server")
	// ErrStepUpRequired: the operation needs a recent step-up.
	ErrStepUpRequired = errors.New("passkey: a recent step-up is required")
	// ErrNoCeremony: there is no pending ceremony for this session and user
	// (never begun, expired, already used, or begun in another session).
	ErrNoCeremony = errors.New("passkey: no pending passkey ceremony")
	// ErrInvalidResponse: the browser response is not valid JSON of
	// acceptable size.
	ErrInvalidResponse = errors.New("passkey: malformed passkey response")
	// ErrVerification: the response failed WebAuthn verification (origin,
	// RP ID, challenge, signature, unknown passkey, ...).
	ErrVerification = errors.New("passkey: the passkey response could not be verified")
	// ErrSignCountRegression: the authenticator's signature counter went
	// backwards (possible cloned authenticator); the assertion is refused.
	ErrSignCountRegression = errors.New("passkey: signature counter regression (possible cloned authenticator)")
	// ErrNotFound: no such passkey of this user.
	ErrNotFound = errors.New("passkey: passkey not found")
	// ErrTooMany: the user already has MaxPasskeys passkeys.
	ErrTooMany = errors.New("passkey: too many passkeys")
	// ErrNoPasskeys: an assertion was requested for a user without passkeys.
	ErrNoPasskeys = errors.New("passkey: the user has no passkeys")
)

// NameError is an invalid passkey name.
type NameError struct{ Message string }

func (e *NameError) Error() string { return "passkey: invalid name: " + e.Message }

// EventKind is the kind of a security-relevant passkey event.
type EventKind string

// Event kinds.
const (
	EventRegistered       EventKind = "registered"
	EventRemoved          EventKind = "removed"
	EventSignCountAnomaly EventKind = "sign_count_anomaly"
)

// Event is reported to the Notifier after the change is committed.
type Event struct {
	Kind      EventKind
	UserID    uuid.UUID
	PasskeyID uuid.UUID
	Name      string
	IP        netip.Addr
	// StoredCount and ReceivedCount are set for EventSignCountAnomaly.
	StoredCount   uint32
	ReceivedCount uint32
}

// Notifier receives committed passkey events (security alerts, task 5.5).
// Implementations must not block for long.
type Notifier interface {
	PasskeyEvent(ctx context.Context, ev Event)
}

// NopNotifier ignores every event.
type NopNotifier struct{}

// PasskeyEvent does nothing.
func (NopNotifier) PasskeyEvent(context.Context, Event) {}

// Options configure New.
type Options struct {
	DB      auth.DB
	Auditor *audit.Auditor
	// BaseURL is server.base_url (the RP ID and origin come from it).
	BaseURL string
	// Dev additionally accepts DevOrigin for a localhost RP ID.
	Dev bool
	// StepUpWindow is security.stepup_max_age (0: 5 minutes).
	StepUpWindow time.Duration
	// Factors reports the user's other enrolled factors for the removal
	// guard (nil: only passkeys are considered).
	Factors *auth.SecondFactors
	// RemovalGuard decides whether a passkey may be deleted (nil:
	// auth.AllowFactorRemoval until the 2FA policy of task 1.9).
	RemovalGuard auth.FactorRemovalGuard
	// Notifier receives committed events (nil: NopNotifier).
	Notifier Notifier
	Logger   *slog.Logger
}

// Service implements passkeys as a second factor: registration,
// assertion and management. It is safe for concurrent use and implements
// auth.FactorChecker.
type Service struct {
	o  Options
	rp RelyingParty
	wa *webauthn.WebAuthn
}

// New returns the service. When the base URL does not allow WebAuthn the
// service still works for listing and deleting, but every ceremony fails
// with ErrUnavailable.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Auditor == nil {
		return nil, errors.New("passkey: DB and Auditor are required")
	}
	if o.StepUpWindow <= 0 || o.StepUpWindow > defaultStepUpWindow {
		o.StepUpWindow = defaultStepUpWindow
	}
	if o.RemovalGuard == nil {
		o.RemovalGuard = auth.AllowFactorRemoval{}
	}
	if o.Notifier == nil {
		o.Notifier = NopNotifier{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Service{o: o, rp: RelyingPartyFor(o.BaseURL, o.Dev)}
	if s.rp.Available {
		wa, err := newWebAuthn(s.rp)
		if err != nil {
			return nil, err
		}
		s.wa = wa
	}
	return s, nil
}

// newWebAuthn configures the library for rp (D-109): attestation "none",
// residentKey "preferred", userVerification UserVerification, server-side
// enforced CeremonyTimeout, no cross-origin use.
func newWebAuthn(rp RelyingParty) (*webauthn.WebAuthn, error) {
	timeouts := webauthn.TimeoutConfig{Enforce: true, Timeout: CeremonyTimeout, TimeoutUVD: CeremonyTimeout}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  rp.ID,
		RPDisplayName:         RPDisplayName,
		RPOrigins:             rp.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyNotRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementPreferred,
			UserVerification:   UserVerification,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeouts, Registration: timeouts},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: configure WebAuthn: %w", err)
	}
	return wa, nil
}

// Status returns the relying party (availability, reason, RP ID, origins).
func (s *Service) Status() RelyingParty {
	rp := s.rp
	rp.Origins = append([]string(nil), rp.Origins...)
	return rp
}

// Kind implements auth.FactorChecker.
func (s *Service) Kind() auth.Factor { return auth.FactorPasskey }

// HasFactor implements auth.FactorChecker: whether userID has a passkey.
// A registered passkey counts even when WebAuthn is currently unavailable
// (the user is enrolled; the base URL may be fixed).
func (s *Service) HasFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := sqlc.New(s.o.DB).UserHasWebAuthnCredential(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("passkey: check enrollment: %w", err)
	}
	return ok, nil
}

// Passkey is a registered passkey as shown to its owner. The public key
// and the credential id are never exposed.
type Passkey struct {
	ID                 uuid.UUID
	Name               string
	CreatedAt          time.Time
	LastUsedAt         *time.Time
	Transports         []string
	AAGUID             *uuid.UUID
	BackupEligible     bool
	BackupState        bool
	UserVerified       bool
	SignCountAnomalyAt *time.Time
}

func passkeyOf(r *sqlc.WebauthnCredential) Passkey {
	return Passkey{
		ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt,
		Transports: append([]string{}, r.Transports...), AAGUID: r.Aaguid,
		BackupEligible: r.BackupEligible, BackupState: r.BackupState, UserVerified: r.UserVerified,
		SignCountAnomalyAt: r.SignCountAnomalyAt,
	}
}

// List returns the passkeys of userID, oldest first.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Passkey, error) {
	rows, err := sqlc.New(s.o.DB).ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("passkey: list: %w", err)
	}
	out := make([]Passkey, 0, len(rows))
	for i := range rows {
		out = append(out, passkeyOf(&rows[i]))
	}
	return out, nil
}

// ValidateName trims name and checks it: 1 to MaxNameLength characters,
// valid UTF-8, no control characters.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case !utf8.ValidString(name):
		return "", &NameError{"must be valid UTF-8"}
	case name == "":
		return "", &NameError{"must not be empty"}
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", &NameError{fmt.Sprintf("must be at most %d characters", MaxNameLength)}
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return "", &NameError{"must not contain control characters"}
	}
	return name, nil
}

// ValidateResponseJSON checks a browser response: at most
// MaxResponseBytes of valid JSON with an object at the top.
func ValidateResponseJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || len(b) > MaxResponseBytes || b[0] != '{' || !json.Valid(b) {
		return ErrInvalidResponse
	}
	return nil
}

// --- WebAuthn user ----------------------------------------------------------

// waUser adapts a bekup user to webauthn.User. The user handle is the
// user's random (v4) UUID: 16 bytes, no personal data (D-109).
type waUser struct {
	id      uuid.UUID
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte {
	id := u.id
	return id[:]
}
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func credentialOf(r *sqlc.WebauthnCredential) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, 0, len(r.Transports))
	for _, t := range r.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	c := webauthn.Credential{
		ID:                append([]byte(nil), r.CredentialID...),
		PublicKey:         append([]byte(nil), r.PublicKey...),
		AttestationType:   r.AttestationType,
		AttestationFormat: r.AttestationFormat,
		Transport:         transports,
		Flags: webauthn.CredentialFlags{
			UserPresent: r.UserPresent, UserVerified: r.UserVerified,
			BackupEligible: r.BackupEligible, BackupState: r.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			SignCount:  uint32(r.SignCount), //nolint:gosec // G115: sign_count is CHECKed to 0..2^32-1
			Attachment: protocol.AuthenticatorAttachment(r.Attachment),
		},
	}
	if r.Aaguid != nil {
		g := *r.Aaguid
		c.Authenticator.AAGUID = g[:]
	}
	return c
}

// loadUser returns the WebAuthn user and the stored passkeys of userID.
func (s *Service) loadUser(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (*waUser, []sqlc.WebauthnCredential, error) {
	u, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: load user: %w", err)
	}
	rows, err := q.ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: load passkeys: %w", err)
	}
	wu := &waUser{id: u.ID, name: u.Email, display: u.DisplayName}
	if wu.display == "" {
		wu.display = u.Email
	}
	for i := range rows {
		wu.creds = append(wu.creds, credentialOf(&rows[i]))
	}
	return wu, rows, nil
}

// --- ceremonies -------------------------------------------------------------

func (s *Service) available() error {
	if !s.rp.Available || s.wa == nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) requireStepUp(p auth.Principal) error {
	if !p.Valid() || !p.HasRecentStepUp(s.o.StepUpWindow) {
		return ErrStepUpRequired
	}
	return nil
}

// putCeremony stores sd as the pending ceremony of kind for the session
// (replacing an older one) and purges expired ceremonies.
func putCeremony(ctx context.Context, q *sqlc.Queries, sessionID, userID uuid.UUID, kind string, sd *webauthn.SessionData) error {
	data, err := json.Marshal(sd)
	if err != nil {
		return fmt.Errorf("passkey: encode ceremony: %w", err)
	}
	if _, err := q.DeleteExpiredWebAuthnCeremonies(ctx); err != nil {
		return fmt.Errorf("passkey: purge ceremonies: %w", err)
	}
	if err := q.PutWebAuthnCeremony(ctx, sqlc.PutWebAuthnCeremonyParams{
		SessionID: sessionID, UserID: userID, Kind: kind, Data: data,
		ExpiresAt: time.Now().Add(CeremonyTimeout),
	}); err != nil {
		return fmt.Errorf("passkey: store ceremony: %w", err)
	}
	return nil
}

// takeCeremony consumes the pending ceremony (single use, committed at
// once so that a failed verification cannot be retried with the same
// challenge).
func (s *Service) takeCeremony(ctx context.Context, sessionID, userID uuid.UUID, kind string) (webauthn.SessionData, error) {
	row, err := sqlc.New(s.o.DB).TakeWebAuthnCeremony(ctx, sqlc.TakeWebAuthnCeremonyParams{
		SessionID: sessionID, UserID: userID, Kind: kind,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return webauthn.SessionData{}, ErrNoCeremony
	}
	if err != nil {
		return webauthn.SessionData{}, fmt.Errorf("passkey: take ceremony: %w", err)
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(row.Data, &sd); err != nil {
		return webauthn.SessionData{}, fmt.Errorf("passkey: decode ceremony: %w", err)
	}
	return sd, nil
}

// fail records a failed ceremony step and returns ret (or the audit
// error: audit failures fail closed).
func (s *Service) fail(ctx context.Context, userID uuid.UUID, action audit.Action, reason string, ret error) error {
	d := audit.Details{"reason": reason}
	if _, err := s.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.UserActor(userID), Action: action, IP: auth.RequestMetaFrom(ctx).IP,
		TargetType: "user", TargetID: userID.String(), Details: d,
	}); err != nil {
		return err
	}
	return ret
}

// BeginRegistration starts registering a passkey for the caller (needs a
// recent step-up) and returns the PublicKeyCredentialCreationOptions as
// JSON ({"publicKey": {...}}): attestation "none", residentKey
// "preferred", userVerification UserVerification, the existing passkeys
// excluded, timeout CeremonyTimeout. The challenge is stored server-side,
// bound to the caller's session.
func (s *Service) BeginRegistration(ctx context.Context, p auth.Principal) (json.RawMessage, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if err := s.requireStepUp(p); err != nil {
		return nil, err
	}
	user, rows, err := s.loadUser(ctx, sqlc.New(s.o.DB), p.UserID)
	if err != nil {
		return nil, err
	}
	if len(rows) >= MaxPasskeys {
		return nil, ErrTooMany
	}
	creation, sd, err := s.wa.BeginRegistration(user,
		webauthn.WithExclusions(webauthn.Credentials(user.creds).CredentialDescriptors()),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, fmt.Errorf("passkey: begin registration: %w", err)
	}
	out, err := json.Marshal(creation)
	if err != nil {
		return nil, fmt.Errorf("passkey: encode creation options: %w", err)
	}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if err := putCeremony(ctx, q, p.SessionID, p.UserID, kindRegistration, sd); err != nil {
			return err
		}
		_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthPasskeyRegistrationBegun,
			IP: auth.RequestMetaFrom(ctx).IP, TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{"session_id": p.SessionID.String(), "existing_passkeys": len(rows)},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FinishRegistration verifies the browser's attestation response against
// the pending registration of the caller's session (consumed whatever the
// outcome) and stores the passkey under name. Needs a recent step-up.
func (s *Service) FinishRegistration(ctx context.Context, p auth.Principal, name string, response []byte) (Passkey, error) {
	if err := s.available(); err != nil {
		return Passkey{}, err
	}
	if err := s.requireStepUp(p); err != nil {
		return Passkey{}, err
	}
	name, err := ValidateName(name)
	if err != nil {
		return Passkey{}, err
	}
	if err := ValidateResponseJSON(response); err != nil {
		return Passkey{}, err
	}
	const failed = audit.ActionAuthPasskeyRegistrationFailed
	sd, err := s.takeCeremony(ctx, p.SessionID, p.UserID, kindRegistration)
	if errors.Is(err, ErrNoCeremony) {
		return Passkey{}, s.fail(ctx, p.UserID, failed, "no_ceremony", err)
	}
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "passkey registration response rejected", slog.String("user_id", p.UserID.String()), slog.Any("error", err))
		return Passkey{}, s.fail(ctx, p.UserID, failed, "invalid_response", ErrVerification)
	}
	user, _, err := s.loadUser(ctx, sqlc.New(s.o.DB), p.UserID)
	if err != nil {
		return Passkey{}, err
	}
	cred, err := s.wa.CreateCredential(user, sd, parsed)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "passkey registration verification failed", slog.String("user_id", p.UserID.String()), slog.Any("error", err))
		return Passkey{}, s.fail(ctx, p.UserID, failed, "verification_failed", ErrVerification)
	}
	if n := len(cred.ID); n < 16 || n > 1023 || len(cred.PublicKey) == 0 || len(cred.PublicKey) > 4096 {
		return Passkey{}, s.fail(ctx, p.UserID, failed, "unsupported_credential", ErrVerification)
	}
	params := insertParams(p.UserID, name, cred)
	var row sqlc.WebauthnCredential
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := q.LockUserForFactorChange(ctx, p.UserID); err != nil {
			return fmt.Errorf("passkey: lock user: %w", err)
		}
		n, err := q.CountWebAuthnCredentials(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("passkey: count passkeys: %w", err)
		}
		if n >= MaxPasskeys {
			return ErrTooMany
		}
		row, err = q.InsertWebAuthnCredential(ctx, params)
		if isUniqueViolation(err) {
			return errDuplicate
		}
		if err != nil {
			return fmt.Errorf("passkey: store passkey: %w", err)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFAEnrolled,
			IP: auth.RequestMetaFrom(ctx).IP, TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{
				"factor": string(auth.FactorPasskey), "passkey_id": row.ID.String(), "name": row.Name,
				"aaguid": aaguidString(row.Aaguid), "attestation_format": row.AttestationFormat,
				"user_verified": row.UserVerified, "backup_eligible": row.BackupEligible,
			},
		})
		return err
	})
	switch {
	case errors.Is(err, errDuplicate):
		return Passkey{}, s.fail(ctx, p.UserID, failed, "already_registered", ErrVerification)
	case errors.Is(err, ErrTooMany):
		return Passkey{}, s.fail(ctx, p.UserID, failed, "too_many", ErrTooMany)
	case err != nil:
		return Passkey{}, err
	}
	s.o.Notifier.PasskeyEvent(ctx, Event{
		Kind: EventRegistered, UserID: p.UserID, PasskeyID: row.ID, Name: row.Name, IP: auth.RequestMetaFrom(ctx).IP,
	})
	return passkeyOf(&row), nil
}

var errDuplicate = errors.New("passkey: credential already registered")

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func aaguidString(g *uuid.UUID) string {
	if g == nil {
		return ""
	}
	return g.String()
}

// insertParams maps a verified credential to its row, bounding the
// client-influenced text fields to the column limits.
func insertParams(userID uuid.UUID, name string, c *webauthn.Credential) sqlc.InsertWebAuthnCredentialParams {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		if s := string(t); s != "" && len(s) <= 32 && len(transports) < 16 {
			transports = append(transports, s)
		}
	}
	p := sqlc.InsertWebAuthnCredentialParams{
		UserID: userID, CredentialID: c.ID, PublicKey: c.PublicKey,
		AttestationType: bounded(c.AttestationType, 64), AttestationFormat: bounded(c.AttestationFormat, 64),
		SignCount: int64(c.Authenticator.SignCount), Transports: transports,
		Attachment:  bounded(string(c.Authenticator.Attachment), 32),
		UserPresent: c.Flags.UserPresent, UserVerified: c.Flags.UserVerified,
		BackupEligible: c.Flags.BackupEligible, BackupState: c.Flags.BackupState && c.Flags.BackupEligible,
		Name: name,
	}
	if g, err := uuid.FromBytes(c.Authenticator.AAGUID); err == nil {
		p.Aaguid = &g
	}
	return p
}

func bounded(s string, n int) string {
	if len(s) > n || !utf8.ValidString(s) {
		return ""
	}
	return s
}

// BeginAssertion starts a passkey check (second factor of the two-step
// login and step-up, task 1.9) for userID, bound to sessionID, and returns
// the PublicKeyCredentialRequestOptions as JSON ({"publicKey": {...}})
// with the user's passkeys as allowCredentials. The caller has already
// established who the user is (password step) and owns sessionID.
func (s *Service) BeginAssertion(ctx context.Context, sessionID, userID uuid.UUID) (json.RawMessage, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if sessionID == uuid.Nil || userID == uuid.Nil {
		return nil, errors.New("passkey: session and user are required")
	}
	user, _, err := s.loadUser(ctx, sqlc.New(s.o.DB), userID)
	if err != nil {
		return nil, err
	}
	if len(user.creds) == 0 {
		return nil, ErrNoPasskeys
	}
	assertion, sd, err := s.wa.BeginLogin(user, webauthn.WithUserVerification(UserVerification))
	if err != nil {
		return nil, fmt.Errorf("passkey: begin assertion: %w", err)
	}
	out, err := json.Marshal(assertion)
	if err != nil {
		return nil, fmt.Errorf("passkey: encode request options: %w", err)
	}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		return putCeremony(ctx, q, sessionID, userID, kindAssertion, sd)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FinishAssertion verifies the browser's assertion response against the
// pending assertion of sessionID and userID (single use: consumed whatever
// the outcome). The library checks challenge, origin, RP ID, user
// presence, the signature and that the passkey belongs to the user. A
// signature counter that does not increase (unless it is 0 on both sides:
// authenticators without a counter) is refused as a possible clone
// (ErrSignCountRegression), recorded on the passkey, audited and reported
// to the Notifier. Successes and failures are audited. The caller (task
// 1.9) elevates the session on success; the returned passkey's
// UserVerified is the UV flag of this assertion (D-116: only a verified
// user sets the session's second-factor time).
func (s *Service) FinishAssertion(ctx context.Context, sessionID, userID uuid.UUID, response []byte) (Passkey, error) {
	if err := s.available(); err != nil {
		return Passkey{}, err
	}
	if err := ValidateResponseJSON(response); err != nil {
		return Passkey{}, s.fail(ctx, userID, audit.ActionAuthPasskeyAssertionFailed, "invalid_response", err)
	}
	const failed = audit.ActionAuthPasskeyAssertionFailed
	sd, err := s.takeCeremony(ctx, sessionID, userID, kindAssertion)
	if errors.Is(err, ErrNoCeremony) {
		return Passkey{}, s.fail(ctx, userID, failed, "no_ceremony", err)
	}
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "passkey assertion response rejected", slog.String("user_id", userID.String()), slog.Any("error", err))
		return Passkey{}, s.fail(ctx, userID, failed, "invalid_response", ErrVerification)
	}
	user, rows, err := s.loadUser(ctx, sqlc.New(s.o.DB), userID)
	if err != nil {
		return Passkey{}, err
	}
	cred, err := s.wa.ValidateLogin(user, sd, parsed)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "passkey assertion verification failed", slog.String("user_id", userID.String()), slog.Any("error", err))
		return Passkey{}, s.fail(ctx, userID, failed, "verification_failed", ErrVerification)
	}
	var stored *sqlc.WebauthnCredential
	for i := range rows {
		if bytes.Equal(rows[i].CredentialID, cred.ID) {
			stored = &rows[i]
			break
		}
	}
	if stored == nil {
		return Passkey{}, s.fail(ctx, userID, failed, "unknown_passkey", ErrVerification)
	}
	received := parsed.Response.AuthenticatorData.Counter
	flags := parsed.Response.AuthenticatorData.Flags
	if cred.Authenticator.CloneWarning {
		return Passkey{}, s.signCountAnomaly(ctx, userID, stored, received)
	}
	var row sqlc.WebauthnCredential
	regressed := false
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		row, err = q.RecordWebAuthnAssertion(ctx, sqlc.RecordWebAuthnAssertionParams{
			ID: stored.ID, UserID: userID, SignCount: int64(received),
			UserPresent: cred.Flags.UserPresent, UserVerified: cred.Flags.UserVerified,
			BackupState: cred.Flags.BackupState && stored.BackupEligible,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent assertion advanced the counter first (or the
			// passkey was deleted meanwhile): refuse.
			regressed = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("passkey: record assertion: %w", err)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(userID), Action: audit.ActionAuthPasskeyAssertionSucceeded,
			IP: auth.RequestMetaFrom(ctx).IP, TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{
				"passkey_id": row.ID.String(), "session_id": sessionID.String(),
				"user_verified": flags.HasUserVerified(), "sign_count": received,
			},
		})
		return err
	})
	if err != nil {
		return Passkey{}, err
	}
	if regressed {
		return Passkey{}, s.signCountAnomaly(ctx, userID, stored, received)
	}
	out := passkeyOf(&row)
	// UserVerified describes this assertion (signed authenticator data),
	// whatever the row recorded before (D-116).
	out.UserVerified = flags.HasUserVerified()
	return out, nil
}

// signCountAnomaly records a refused assertion with a regressed counter
// and returns ErrSignCountRegression (or the audit/DB error).
func (s *Service) signCountAnomaly(ctx context.Context, userID uuid.UUID, stored *sqlc.WebauthnCredential, received uint32) error {
	storedCount := uint32(stored.SignCount) //nolint:gosec // G115: sign_count is CHECKed to 0..2^32-1
	ip := auth.RequestMetaFrom(ctx).IP
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := q.MarkWebAuthnSignCountAnomaly(ctx, sqlc.MarkWebAuthnSignCountAnomalyParams{ID: stored.ID, UserID: userID}); err != nil {
			return fmt.Errorf("passkey: mark sign count anomaly: %w", err)
		}
		_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(userID), Action: audit.ActionAuthPasskeySignCountAnomaly,
			IP: ip, TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{
				"passkey_id": stored.ID.String(), "name": stored.Name,
				"stored_sign_count": storedCount, "received_sign_count": received,
			},
		})
		return err
	})
	if err != nil {
		return err
	}
	s.o.Logger.WarnContext(ctx, "passkey signature counter regression: possible cloned authenticator",
		slog.String("user_id", userID.String()), slog.String("passkey_id", stored.ID.String()),
		slog.Uint64("stored_sign_count", uint64(storedCount)), slog.Uint64("received_sign_count", uint64(received)))
	s.o.Notifier.PasskeyEvent(ctx, Event{
		Kind: EventSignCountAnomaly, UserID: userID, PasskeyID: stored.ID, Name: stored.Name, IP: ip,
		StoredCount: storedCount, ReceivedCount: received,
	})
	return ErrSignCountRegression
}

// --- management -------------------------------------------------------------

// Rename renames one of the caller's passkeys (needs a recent step-up).
func (s *Service) Rename(ctx context.Context, p auth.Principal, id uuid.UUID, name string) (Passkey, error) {
	if err := s.requireStepUp(p); err != nil {
		return Passkey{}, err
	}
	name, err := ValidateName(name)
	if err != nil {
		return Passkey{}, err
	}
	var row sqlc.WebauthnCredential
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		row, err = q.RenameWebAuthnCredential(ctx, sqlc.RenameWebAuthnCredentialParams{ID: id, UserID: p.UserID, Name: name})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("passkey: rename: %w", err)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthPasskeyRenamed,
			IP: auth.RequestMetaFrom(ctx).IP, TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{"passkey_id": id.String(), "name": name},
		})
		return err
	})
	if err != nil {
		return Passkey{}, err
	}
	return passkeyOf(&row), nil
}

// Delete removes one of the caller's passkeys (needs a recent step-up).
// The RemovalGuard (task 1.9's last-factor protection) sees the factors
// the user would keep and can refuse. Removing the last factor also
// deletes the recovery codes.
func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	if err := s.requireStepUp(p); err != nil {
		return err
	}
	var row sqlc.WebauthnCredential
	ip := auth.RequestMetaFrom(ctx).IP
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := q.LockUserForFactorChange(ctx, p.UserID); err != nil {
			return fmt.Errorf("passkey: lock user: %w", err)
		}
		n, err := q.CountWebAuthnCredentials(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("passkey: count passkeys: %w", err)
		}
		row, err = q.DeleteWebAuthnCredential(ctx, sqlc.DeleteWebAuthnCredentialParams{ID: id, UserID: p.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("passkey: delete: %w", err)
		}
		remaining, err := s.remainingFactors(ctx, p.UserID, n-1)
		if err != nil {
			return err
		}
		if err := s.o.RemovalGuard.CheckFactorRemoval(ctx, p.UserID, auth.FactorPasskey, remaining); err != nil {
			return err
		}
		factors := make([]string, 0, len(remaining))
		for _, f := range remaining {
			factors = append(factors, string(f))
		}
		// Recovery codes only stand in for a factor (D-111): without any
		// factor left they are deleted.
		var codesDeleted int64
		if len(remaining) == 0 {
			if codesDeleted, err = q.DeleteRecoveryCodes(ctx, p.UserID); err != nil {
				return fmt.Errorf("passkey: delete recovery codes: %w", err)
			}
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFARemoved,
			IP: ip, TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{
				"factor": string(auth.FactorPasskey), "passkey_id": row.ID.String(), "name": row.Name,
				"remaining_passkeys": n - 1, "remaining_factors": factors,
				"recovery_codes_deleted": codesDeleted,
			},
		})
		return err
	})
	if err != nil {
		return err
	}
	s.o.Notifier.PasskeyEvent(ctx, Event{Kind: EventRemoved, UserID: p.UserID, PasskeyID: row.ID, Name: row.Name, IP: ip})
	return nil
}

// remainingFactors are the factor kinds the user keeps after removing a
// passkey, with passkeysLeft passkeys remaining.
func (s *Service) remainingFactors(ctx context.Context, userID uuid.UUID, passkeysLeft int64) ([]auth.Factor, error) {
	var out []auth.Factor
	if s.o.Factors != nil {
		enrolled, err := s.o.Factors.Enrolled(ctx, userID)
		if err != nil {
			return nil, err
		}
		for _, f := range enrolled {
			if f != auth.FactorPasskey {
				out = append(out, f)
			}
		}
	}
	if passkeysLeft > 0 {
		out = append(out, auth.FactorPasskey)
	}
	return out, nil
}

// Available reports whether passkey ceremonies work with this server
// (auth.PasskeyAsserter).
func (s *Service) Available() bool { return s.available() == nil }

// VerifyAssertion is FinishAssertion for the two-step login and step-up
// (auth.PasskeyAsserter, task 1.9): rejections of the response itself (no
// pending ceremony, malformed or unverifiable response, unknown passkey,
// counter regression, no passkeys) are wrapped with
// auth.ErrAssertionRejected so the caller counts them towards the
// second-factor lockout; other errors (database, audit) are returned as
// they are.
//
// The result reports whether the authenticator verified the user (the UV
// flag of this assertion's signed authenticator data): only such an
// assertion counts as a fresh second factor for restores (D-116).
func (s *Service) VerifyAssertion(ctx context.Context, sessionID, userID uuid.UUID, response []byte) (auth.AssertionResult, error) {
	pk, err := s.FinishAssertion(ctx, sessionID, userID, response)
	switch {
	case err == nil:
		return auth.AssertionResult{UserVerified: pk.UserVerified}, nil
	case errors.Is(err, ErrNoCeremony), errors.Is(err, ErrInvalidResponse), errors.Is(err, ErrVerification),
		errors.Is(err, ErrSignCountRegression), errors.Is(err, ErrNoPasskeys):
		return auth.AssertionResult{}, fmt.Errorf("%w: %w", auth.ErrAssertionRejected, err)
	default:
		return auth.AssertionResult{}, err
	}
}
