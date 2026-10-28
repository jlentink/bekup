package jobkeys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Errors of the service. Presentable messages live in the API layer.
var (
	// ErrNotConfigured: the job has no encryption row (task 3.5 creates it
	// with the job). Backups must not run for such a job.
	ErrNotConfigured = errors.New("jobkeys: the job has no encryption configuration")
	// ErrAlreadyConfigured: InitTx for a job that already has one.
	ErrAlreadyConfigured = errors.New("jobkeys: the job already has an encryption configuration")
	// ErrVersionNotFound: no key with that version exists for the job.
	ErrVersionNotFound = errors.New("jobkeys: no key with this version")
	// ErrNoActiveKey: the job has no active key of the requested mode.
	ErrNoActiveKey = errors.New("jobkeys: the job has no active key for this mode")
	// ErrNoPassphrases: an export of a job without passphrases.
	ErrNoPassphrases = errors.New("jobkeys: the job has no passphrases")
	// ErrActorMismatch: the actor is not the authenticated caller.
	ErrActorMismatch = errors.New("jobkeys: the actor is not the authenticated caller")
	// ErrAdminOnly: a key change by a caller who is not an admin (the policy
	// denies it first; this is the service's own re-check).
	ErrAdminOnly = errors.New("jobkeys: only an admin can change a job's encryption keys")
	// ErrNotOptedOut / ErrAlreadyOptedOut: an opt-in of an encrypted job,
	// an opt-out of an opted-out job.
	ErrNotOptedOut     = errors.New("jobkeys: the job is not opted out")
	ErrAlreadyOptedOut = errors.New("jobkeys: the job is already opted out")
)

// AuditTargetType is the audit target type of job encryption entries (the
// target id is the job id).
const AuditTargetType = "job"

// DB is what the service needs from the app database (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Authorizer is the central policy (*authz.Policy).
type Authorizer interface {
	Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error
}

// Options configure New.
type Options struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	// Authorizer double-checks the caller of every change (the GraphQL
	// layer checks first).
	Authorizer Authorizer
	// Alerts receives security events (opt-out, export); nil logs them.
	// Task 5.5 turns them into security alerts.
	Alerts AlertFunc
	// PassphraseWords is the length of generated passphrases (default and
	// minimum crypto.MinPassphraseWords).
	PassphraseWords int
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logger receives operational events (never key material).
	Logger *slog.Logger
}

// Actor is the user performing a change. It must be the authenticated
// caller of the context.
type Actor struct {
	UserID uuid.UUID
	IP     netip.Addr
}

// Service manages job encryption keys; see the package documentation.
type Service struct {
	o Options
}

// New validates o and returns the service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Authorizer == nil {
		return nil, errors.New("jobkeys: DB, Keyring, Auditor and Authorizer are required")
	}
	if o.PassphraseWords == 0 {
		o.PassphraseWords = crypto.MinPassphraseWords
	}
	if o.PassphraseWords < crypto.MinPassphraseWords || o.PassphraseWords > crypto.MaxPassphraseWords {
		return nil, fmt.Errorf("jobkeys: passphrase length must be %d..%d words", crypto.MinPassphraseWords, crypto.MaxPassphraseWords)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Alerts == nil {
		o.Alerts = logAlert(o.Logger)
	}
	return &Service{o: o}, nil
}

// passphraseAAD binds a sealed passphrase to its job and version.
func passphraseAAD(job uuid.UUID, version int) crypto.AAD {
	return crypto.AAD{Table: "job_passphrases", RowID: job.String(), Field: "v" + strconv.Itoa(version)}
}

// authorize checks op for the caller of ctx and that actor is that caller.
func (s *Service) authorize(ctx context.Context, actor Actor, op authz.Operation, job uuid.UUID) error {
	if err := s.o.Authorizer.Authorize(ctx, op, authz.Job{ID: job}); err != nil {
		return err
	}
	pr, ok := auth.FromContext(ctx)
	if !ok || pr.UserID != actor.UserID || actor.UserID == uuid.Nil {
		return ErrActorMismatch
	}
	// Key changes are admin-only (user decision, M3 review): checked here
	// as well, independently of the policy table.
	if op == authz.OpJobEncryptionKeysEdit && pr.Role != auth.RoleAdmin {
		return ErrAdminOnly
	}
	return nil
}

// --- reading ---------------------------------------------------------------

func passphraseInfo(r *sqlc.JobPassphrase) PassphraseInfo {
	return PassphraseInfo{Version: int(r.Version), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, RetiredAt: r.RetiredAt}
}

func recipientInfo(r *sqlc.JobRecipient) RecipientInfo {
	return RecipientInfo{
		Version: int(r.Version), Recipient: r.Recipient, Fingerprint: r.Fingerprint, Label: r.Label,
		Generated: r.Generated, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, RetiredAt: r.RetiredAt,
	}
}

// Status returns the encryption state of a job. The caller authorizes the
// read (job.view: nested under the job); it contains no key material.
func (s *Service) Status(ctx context.Context, job uuid.UUID) (Status, error) {
	return s.status(ctx, sqlc.New(s.o.DB), job)
}

func (s *Service) status(ctx context.Context, q *sqlc.Queries, job uuid.UUID) (Status, error) {
	st := Status{JobID: job}
	enc, err := q.GetJobEncryption(ctx, job)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("jobkeys: load encryption: %w", err)
	}
	st.Configured, st.Mode, st.UpdatedAt = true, Mode(enc.Mode), enc.UpdatedAt
	if st.Mode == ModeNone {
		st.OptedOut, st.OptedOutBy, st.OptedOutAt = true, enc.OptedOutBy, enc.OptedOutAt
		if enc.OptOutReason != nil {
			st.OptOutReason = *enc.OptOutReason
		}
	}
	p, err := q.GetActiveJobPassphrase(ctx, job)
	switch {
	case err == nil:
		info := passphraseInfo(&p)
		st.Passphrase = &info
	case !errors.Is(err, pgx.ErrNoRows):
		return st, fmt.Errorf("jobkeys: load passphrase: %w", err)
	}
	r, err := q.GetActiveJobRecipient(ctx, job)
	switch {
	case err == nil:
		info := recipientInfo(&r)
		st.Recipient = &info
	case !errors.Is(err, pgx.ErrNoRows):
		return st, fmt.Errorf("jobkeys: load recipient: %w", err)
	}
	return st, nil
}

// Passphrases lists the passphrase versions of a job (never the values).
func (s *Service) Passphrases(ctx context.Context, job uuid.UUID) ([]PassphraseInfo, error) {
	rows, err := sqlc.New(s.o.DB).ListJobPassphrases(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("jobkeys: list passphrases: %w", err)
	}
	out := make([]PassphraseInfo, len(rows))
	for i := range rows {
		out[i] = passphraseInfo(&rows[i])
		crypto.Zero(rows[i].SealedPassphrase)
	}
	return out, nil
}

// Recipients lists the recipient versions of a job.
func (s *Service) Recipients(ctx context.Context, job uuid.UUID) ([]RecipientInfo, error) {
	rows, err := sqlc.New(s.o.DB).ListJobRecipients(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("jobkeys: list recipients: %w", err)
	}
	out := make([]RecipientInfo, len(rows))
	for i := range rows {
		out[i] = recipientInfo(&rows[i])
	}
	return out, nil
}

// RecipientForVersion returns one recipient version (restore: which
// identity the artifact needs).
func (s *Service) RecipientForVersion(ctx context.Context, job uuid.UUID, version int) (RecipientInfo, error) {
	if version < 1 || version > 1_000_000 {
		return RecipientInfo{}, ErrVersionNotFound
	}
	r, err := sqlc.New(s.o.DB).GetJobRecipient(ctx, sqlc.GetJobRecipientParams{JobID: job, Version: int32(version)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RecipientInfo{}, ErrVersionNotFound
	}
	if err != nil {
		return RecipientInfo{}, fmt.Errorf("jobkeys: load recipient: %w", err)
	}
	return recipientInfo(&r), nil
}

// open decrypts a sealed passphrase row.
func (s *Service) open(r *sqlc.JobPassphrase) (*crypto.Secret, error) {
	defer crypto.Zero(r.SealedPassphrase)
	pt, err := s.o.Keyring.Open(r.SealedPassphrase, passphraseAAD(r.JobID, int(r.Version)))
	if err != nil {
		return nil, fmt.Errorf("jobkeys: open passphrase v%d of job %s: %w", r.Version, r.JobID, err)
	}
	return crypto.NewSecret(pt), nil
}

// KeyMaterialFor returns the current encryption configuration of a job for
// a new artifact (backup workers; no principal). An opted-out job yields
// Mode ModeNone and no key; a job without configuration ErrNotConfigured.
// The caller must Destroy the result.
func (s *Service) KeyMaterialFor(ctx context.Context, job uuid.UUID) (*KeyMaterial, error) {
	var km *KeyMaterial
	// The share lock keeps key changes out until the key is read: the mode
	// and the key belong together.
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		enc, err := q.GetJobEncryptionForShare(ctx, job)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotConfigured
		}
		if err != nil {
			return fmt.Errorf("jobkeys: load encryption: %w", err)
		}
		switch Mode(enc.Mode) {
		case ModeNone:
			km = &KeyMaterial{JobID: job, Mode: ModeNone}
		case ModePassphrase:
			p, err := q.GetActiveJobPassphrase(ctx, job)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoActiveKey
			}
			if err != nil {
				return fmt.Errorf("jobkeys: load passphrase: %w", err)
			}
			sec, err := s.open(&p)
			if err != nil {
				return err
			}
			km = &KeyMaterial{JobID: job, Mode: ModePassphrase, Passphrase: sec, PassphraseVersion: int(p.Version)}
		case ModeRecipient:
			r, err := q.GetActiveJobRecipient(ctx, job)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoActiveKey
			}
			if err != nil {
				return fmt.Errorf("jobkeys: load recipient: %w", err)
			}
			km = &KeyMaterial{JobID: job, Mode: ModeRecipient, Recipients: []string{r.Recipient},
				RecipientVersion: int(r.Version), RecipientFingerprint: r.Fingerprint}
		default:
			return fmt.Errorf("jobkeys: unknown mode %q", enc.Mode)
		}
		return nil
	})
	if err != nil {
		km.Destroy()
		return nil, err
	}
	return km, nil
}

// PassphraseForVersion returns one passphrase version, active or retired
// (restore and verification of an artifact, task 4.1; no principal: the
// caller authorized the restore). The caller must Destroy the result.
func (s *Service) PassphraseForVersion(ctx context.Context, job uuid.UUID, version int) (*crypto.Secret, error) {
	if version < 1 || version > 1_000_000 {
		return nil, ErrVersionNotFound
	}
	p, err := sqlc.New(s.o.DB).GetJobPassphrase(ctx, sqlc.GetJobPassphraseParams{JobID: job, Version: int32(version)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("jobkeys: load passphrase: %w", err)
	}
	return s.open(&p)
}

// --- changing keys ---------------------------------------------------------

// lock ensures the encryption row of job exists and locks it, serializing
// key changes of the job.
func lock(ctx context.Context, q *sqlc.Queries, job uuid.UUID, actor Actor) (sqlc.JobEncryption, error) {
	if err := q.EnsureJobEncryption(ctx, sqlc.EnsureJobEncryptionParams{JobID: job, UpdatedBy: &actor.UserID}); err != nil {
		return sqlc.JobEncryption{}, fmt.Errorf("jobkeys: create encryption row: %w", err)
	}
	enc, err := q.GetJobEncryptionForUpdate(ctx, job)
	if err != nil {
		return sqlc.JobEncryption{}, fmt.Errorf("jobkeys: lock encryption row: %w", err)
	}
	return enc, nil
}

func (s *Service) record(ctx context.Context, q *sqlc.Queries, actor Actor, action audit.Action, job uuid.UUID, details audit.Details) error {
	_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: action, TargetType: AuditTargetType,
		TargetID: job.String(), IP: actor.IP, Details: details,
	})
	return err
}

// storePassphrase seals pass as the next version, retires the previous one
// and switches the job to passphrase mode. It does not take ownership of
// pass.
func (s *Service) storePassphrase(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, pass []byte, generated bool, via string) (PassphraseInfo, error) {
	enc, err := lock(ctx, q, job, actor)
	if err != nil {
		return PassphraseInfo{}, err
	}
	version, err := q.NextJobPassphraseVersion(ctx, job)
	if err != nil {
		return PassphraseInfo{}, fmt.Errorf("jobkeys: next passphrase version: %w", err)
	}
	sealed, err := s.o.Keyring.Seal(pass, passphraseAAD(job, int(version)))
	if err != nil {
		return PassphraseInfo{}, fmt.Errorf("jobkeys: seal passphrase: %w", err)
	}
	if _, err := q.RetireJobPassphrases(ctx, job); err != nil {
		return PassphraseInfo{}, fmt.Errorf("jobkeys: retire passphrase: %w", err)
	}
	row, err := q.InsertJobPassphrase(ctx, sqlc.InsertJobPassphraseParams{
		JobID: job, Version: version, SealedPassphrase: sealed, CreatedBy: &actor.UserID,
	})
	if err != nil {
		return PassphraseInfo{}, fmt.Errorf("jobkeys: store passphrase: %w", err)
	}
	details := audit.Details{"version": version, "generated": generated, "via": via}
	if version > 1 {
		details["previous_version"] = version - 1
	}
	if err := s.record(ctx, q, actor, audit.ActionJobEncryptionPassphraseSet, job, details); err != nil {
		return PassphraseInfo{}, err
	}
	if err := s.record(ctx, q, actor, audit.ActionSecretChanged, job,
		audit.Details{"fields": []string{"job_passphrase"}, "via": string(audit.ActionJobEncryptionPassphraseSet)}); err != nil {
		return PassphraseInfo{}, err
	}
	if err := s.switchMode(ctx, q, actor, job, enc, ModePassphrase); err != nil {
		return PassphraseInfo{}, err
	}
	return passphraseInfo(&row), nil
}

// storeRecipient stores recipient as the next version, retires the
// previous one and switches the job to recipient mode.
func (s *Service) storeRecipient(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, recipient, fingerprint, label string, generated bool, via string) (RecipientInfo, error) {
	enc, err := lock(ctx, q, job, actor)
	if err != nil {
		return RecipientInfo{}, err
	}
	version, err := q.NextJobRecipientVersion(ctx, job)
	if err != nil {
		return RecipientInfo{}, fmt.Errorf("jobkeys: next recipient version: %w", err)
	}
	if _, err := q.RetireJobRecipients(ctx, job); err != nil {
		return RecipientInfo{}, fmt.Errorf("jobkeys: retire recipient: %w", err)
	}
	row, err := q.InsertJobRecipient(ctx, sqlc.InsertJobRecipientParams{
		JobID: job, Version: version, Recipient: recipient, Fingerprint: fingerprint, Label: label,
		Generated: generated, CreatedBy: &actor.UserID,
	})
	if err != nil {
		return RecipientInfo{}, fmt.Errorf("jobkeys: store recipient: %w", err)
	}
	details := audit.Details{
		"version": version, "recipient": recipient, "fingerprint": fingerprint, "label": label,
		"generated": generated, "via": via,
	}
	if version > 1 {
		details["previous_version"] = version - 1
	}
	if err := s.record(ctx, q, actor, audit.ActionJobEncryptionRecipientSet, job, details); err != nil {
		return RecipientInfo{}, err
	}
	if err := s.switchMode(ctx, q, actor, job, enc, ModeRecipient); err != nil {
		return RecipientInfo{}, err
	}
	return recipientInfo(&row), nil
}

// switchMode sets the job's mode to a key mode. Leaving an opt-out is
// recorded as job.encryption_opted_in (setting a key re-enables
// encryption: a tightening, so the key-edit right suffices).
func (s *Service) switchMode(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, enc sqlc.JobEncryption, to Mode) error {
	from := Mode(enc.Mode)
	if from == to {
		return nil
	}
	if _, err := q.SetJobEncryptionMode(ctx, sqlc.SetJobEncryptionModeParams{
		JobID: job, Mode: sqlc.JobEncryptionMode(to), UpdatedBy: &actor.UserID,
	}); err != nil {
		return fmt.Errorf("jobkeys: set mode: %w", err)
	}
	if from == ModeNone {
		return s.record(ctx, q, actor, audit.ActionJobEncryptionOptedIn, job, audit.Details{"mode": string(to)})
	}
	return nil
}

// SetPassphrase stores a passphrase chosen by the actor as the job's next
// version (the previous version is retired, never deleted) and switches
// the job to passphrase mode. It consumes pass: the secret is destroyed
// whether or not the call succeeds. Needs job.encryption_keys_edit.
func (s *Service) SetPassphrase(ctx context.Context, actor Actor, job uuid.UUID, pass *crypto.Secret) (PassphraseInfo, error) {
	defer pass.Destroy()
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
		return PassphraseInfo{}, err
	}
	if err := validatePassphrase(pass.Bytes()); err != nil {
		return PassphraseInfo{}, err
	}
	var info PassphraseInfo
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		info, err = s.storePassphrase(ctx, q, actor, job, pass.Bytes(), false, "set")
		return err
	})
	return info, err
}

// GeneratePassphrase generates a strong passphrase (crypto.
// GeneratePassphrase, D-088), stores it sealed as the job's next version
// and returns it once for display. Needs job.encryption_keys_edit. The
// caller must Destroy the result.
func (s *Service) GeneratePassphrase(ctx context.Context, actor Actor, job uuid.UUID) (*GeneratedPassphrase, error) {
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
		return nil, err
	}
	var out *GeneratedPassphrase
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		out, err = s.generatePassphrase(ctx, q, actor, job, "generate")
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) generatePassphrase(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, via string) (*GeneratedPassphrase, error) {
	pass, err := crypto.GeneratePassphrase(s.o.PassphraseWords)
	if err != nil {
		return nil, fmt.Errorf("jobkeys: %w", err)
	}
	info, err := s.storePassphrase(ctx, q, actor, job, pass.Bytes(), true, via)
	if err != nil {
		pass.Destroy()
		return nil, err
	}
	return &GeneratedPassphrase{Info: info, Passphrase: pass}, nil
}

// SetRecipient stores an age X25519 recipient (public key) as the job's
// next recipient version and switches the job to recipient mode. Needs
// job.encryption_keys_edit.
func (s *Service) SetRecipient(ctx context.Context, actor Actor, job uuid.UUID, recipient, label string) (RecipientInfo, error) {
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
		return RecipientInfo{}, err
	}
	canonical, fp, err := parseRecipient(recipient)
	if err != nil {
		return RecipientInfo{}, err
	}
	if label, err = cleanText("label", label, 0, MaxLabelRunes); err != nil {
		return RecipientInfo{}, err
	}
	var info RecipientInfo
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		info, err = s.storeRecipient(ctx, q, actor, job, canonical, fp, label, false, "set")
		return err
	})
	return info, err
}

// GenerateAgeKeypair generates an age X25519 keypair, stores only the
// recipient as the job's next version and returns the identity once (plus
// a downloadable identity file). The identity is never persisted or
// logged. Needs job.encryption_keys_edit. The caller must Destroy the
// result.
func (s *Service) GenerateAgeKeypair(ctx context.Context, actor Actor, job uuid.UUID, label string) (*GeneratedKeypair, error) {
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
		return nil, err
	}
	label, err := cleanText("label", label, 0, MaxLabelRunes)
	if err != nil {
		return nil, err
	}
	var out *GeneratedKeypair
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		out, err = s.generateKeypair(ctx, q, actor, job, label, "generate")
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) generateKeypair(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, label, via string) (*GeneratedKeypair, error) {
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		return nil, fmt.Errorf("jobkeys: %w", err)
	}
	canonical, fp, err := parseRecipient(kp.Recipient)
	if err != nil {
		kp.Identity.Destroy()
		return nil, fmt.Errorf("jobkeys: generated recipient: %w", err)
	}
	info, err := s.storeRecipient(ctx, q, actor, job, canonical, fp, label, true, via)
	if err != nil {
		kp.Identity.Destroy()
		return nil, err
	}
	return &GeneratedKeypair{
		Info:         info,
		Identity:     kp.Identity,
		IdentityFile: identityFile(job, info, kp.Identity.Bytes(), s.o.Now()),
		FileName:     "bekup-job-" + job.String() + "-v" + strconv.Itoa(info.Version) + ".agekey",
	}, nil
}

// identityFile renders the age-keygen identity file format.
func identityFile(job uuid.UUID, info RecipientInfo, identity []byte, now time.Time) *crypto.Secret {
	var b bytes.Buffer
	b.Grow(512 + len(identity))
	b.WriteString("# created: " + now.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("# public key: " + info.Recipient + "\n")
	b.WriteString("# bekup job " + job.String() + ", recipient version " + strconv.Itoa(info.Version) + "\n")
	b.WriteString("# SECRET: bekup does not keep this key. Without it the job's backups for this\n")
	b.WriteString("# recipient version cannot be restored. Store it offline (docs/guides/encryption.md).\n")
	b.Write(identity)
	b.WriteByte('\n')
	return crypto.NewSecret(b.Bytes())
}

// --- opt-out and opt-in ----------------------------------------------------

// OptOut turns a job's encryption off (admin only, step-up: needs
// job.encryption_opt_out). It is audited as job.encryption_opted_out and
// security.opt_out, raises AlertEncryptionOptOut and sets the badge flag
// (Status.OptedOut). Keys are kept so the job can opt in again.
func (s *Service) OptOut(ctx context.Context, actor Actor, job uuid.UUID, reason string) (Status, error) {
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionOptOut, job); err != nil {
		return Status{}, err
	}
	reason, err := cleanText("reason", reason, 1, MaxReasonRunes)
	if err != nil {
		return Status{}, err
	}
	var (
		st   Status
		from Mode
	)
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		enc, err := lock(ctx, q, job, actor)
		if err != nil {
			return err
		}
		from = Mode(enc.Mode)
		if from == ModeNone {
			return ErrAlreadyOptedOut
		}
		if _, err := q.OptOutJobEncryption(ctx, sqlc.OptOutJobEncryptionParams{JobID: job, Actor: &actor.UserID, Reason: &reason}); err != nil {
			return fmt.Errorf("jobkeys: opt out: %w", err)
		}
		if err := s.record(ctx, q, actor, audit.ActionJobEncryptionOptedOut, job,
			audit.Details{"from": string(from), "reason": reason}); err != nil {
			return err
		}
		if err := s.record(ctx, q, actor, audit.ActionSecurityOptOut, job,
			audit.Details{"setting": "job.encryption", "from": string(from), "to": string(ModeNone), "reason": reason}); err != nil {
			return err
		}
		st, err = s.status(ctx, q, job)
		return err
	})
	if err != nil {
		return Status{}, err
	}
	s.o.Alerts(ctx, Alert{Kind: AlertEncryptionOptOut, JobID: job, ActorID: actor.UserID,
		Details: map[string]any{"from": string(from), "reason": reason}})
	return st, nil
}

// OptIn turns a job's encryption back on with mode (passphrase or
// recipient), which needs an active key of that mode (otherwise set or
// generate one, which also opts in). Re-enabling encryption is a
// tightening: it needs job.encryption_keys_edit, not the admin opt-out
// right.
func (s *Service) OptIn(ctx context.Context, actor Actor, job uuid.UUID, mode Mode) (Status, error) {
	if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
		return Status{}, err
	}
	if !mode.keyMode() {
		return Status{}, fieldErr("mode", "must be passphrase or recipient")
	}
	var st Status
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		enc, err := lock(ctx, q, job, actor)
		if err != nil {
			return err
		}
		if Mode(enc.Mode) != ModeNone {
			return ErrNotOptedOut
		}
		if mode == ModePassphrase {
			_, err = q.GetActiveJobPassphrase(ctx, job)
		} else {
			_, err = q.GetActiveJobRecipient(ctx, job)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoActiveKey
		}
		if err != nil {
			return fmt.Errorf("jobkeys: load key: %w", err)
		}
		if err := s.switchMode(ctx, q, actor, job, enc, mode); err != nil {
			return err
		}
		st, err = s.status(ctx, q, job)
		return err
	})
	if err != nil {
		return Status{}, err
	}
	return st, nil
}

// --- export ----------------------------------------------------------------

// ExportPassphrases returns every passphrase version of a job, active and
// retired, for offline recovery (admin only, step-up: needs
// job.passphrase_export). The export is audited (fail closed: nothing is
// returned unless the audit entry committed) and raises
// AlertPassphraseExport. The caller must Destroy the result.
func (s *Service) ExportPassphrases(ctx context.Context, actor Actor, job uuid.UUID) (*Export, error) {
	if err := s.authorize(ctx, actor, authz.OpJobPassphraseExport, job); err != nil {
		return nil, err
	}
	ex := &Export{JobID: job, ExportedAt: s.o.Now().UTC()}
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		rows, err := q.ListJobPassphrases(ctx, job)
		if err != nil {
			return fmt.Errorf("jobkeys: list passphrases: %w", err)
		}
		if len(rows) == 0 {
			return ErrNoPassphrases
		}
		versions := make([]int, 0, len(rows))
		for i := range rows {
			sec, err := s.open(&rows[i])
			if err != nil {
				for j := i + 1; j < len(rows); j++ {
					crypto.Zero(rows[j].SealedPassphrase)
				}
				return err
			}
			ex.Versions = append(ex.Versions, ExportedPassphrase{PassphraseInfo: passphraseInfo(&rows[i]), Passphrase: sec})
			versions = append(versions, int(rows[i].Version))
		}
		return s.record(ctx, q, actor, audit.ActionJobPassphrasesExported, job, audit.Details{"versions": versions})
	})
	if err != nil {
		ex.Destroy()
		return nil, err
	}
	versions := make([]int, len(ex.Versions))
	for i, v := range ex.Versions {
		versions[i] = v.Version
	}
	s.o.Alerts(ctx, Alert{Kind: AlertPassphraseExport, JobID: job, ActorID: actor.UserID,
		Details: map[string]any{"versions": versions}})
	return ex, nil
}

// --- job creation (task 3.5) -----------------------------------------------

// Init is the initial encryption of a new job. Encryption is on by
// default; a new job cannot start opted out (OptOut is a separate admin
// action).
type Init struct {
	// Mode is ModePassphrase (default when empty) or ModeRecipient.
	Mode Mode
	// Passphrase (passphrase mode, optional) is a passphrase chosen by the
	// creator; nil generates one. InitTx consumes it.
	Passphrase *crypto.Secret
	// Recipient (recipient mode, optional) is the creator's age recipient;
	// empty generates a keypair.
	Recipient string
	Label     string
}

// InitResult carries what a new job's creator sees once: a generated
// passphrase or a generated keypair (at most one is set).
type InitResult struct {
	Status     Status
	Passphrase *GeneratedPassphrase
	Keypair    *GeneratedKeypair
}

// Destroy zeroes the generated secrets.
func (r *InitResult) Destroy() {
	if r != nil {
		r.Passphrase.Destroy()
		r.Keypair.Destroy()
	}
}

// InitTx sets up the encryption of a job created in the caller's
// transaction q (task 3.5's createJob, which needs job.create). The job
// must not have an encryption configuration yet. The caller must Destroy
// the result.
func (s *Service) InitTx(ctx context.Context, q *sqlc.Queries, actor Actor, job uuid.UUID, in Init) (*InitResult, error) {
	defer in.Passphrase.Destroy()
	if err := s.authorize(ctx, actor, authz.OpJobCreate, job); err != nil {
		return nil, err
	}
	if in.Mode == "" {
		in.Mode = ModePassphrase
	}
	if !in.Mode.keyMode() {
		return nil, fieldErr("mode", "must be passphrase or recipient; only an admin can opt a job out, after creating it")
	}
	// The default (a generated passphrase) comes with job.create; choosing
	// the key is a key change: job.encryption_keys_edit (admin, step-up).
	if in.Mode != ModePassphrase || in.Passphrase != nil || in.Recipient != "" {
		if err := s.authorize(ctx, actor, authz.OpJobEncryptionKeysEdit, job); err != nil {
			return nil, err
		}
	}
	if in.Mode == ModePassphrase && in.Passphrase != nil {
		if err := validatePassphrase(in.Passphrase.Bytes()); err != nil {
			return nil, err
		}
	}
	var (
		canonical, fp string
		err           error
	)
	if in.Mode == ModeRecipient && in.Recipient != "" {
		if canonical, fp, err = parseRecipient(in.Recipient); err != nil {
			return nil, err
		}
	}
	label, err := cleanText("label", in.Label, 0, MaxLabelRunes)
	if err != nil {
		return nil, err
	}
	if _, err := q.InsertJobEncryption(ctx, sqlc.InsertJobEncryptionParams{
		JobID: job, Mode: sqlc.JobEncryptionMode(in.Mode), UpdatedBy: &actor.UserID,
	}); err != nil {
		if serviceutil.IsUniqueViolation(err) {
			return nil, ErrAlreadyConfigured
		}
		return nil, fmt.Errorf("jobkeys: create encryption row: %w", err)
	}
	res := &InitResult{}
	switch {
	case in.Mode == ModePassphrase && in.Passphrase != nil:
		_, err = s.storePassphrase(ctx, q, actor, job, in.Passphrase.Bytes(), false, "create")
	case in.Mode == ModePassphrase:
		res.Passphrase, err = s.generatePassphrase(ctx, q, actor, job, "create")
	case canonical != "":
		_, err = s.storeRecipient(ctx, q, actor, job, canonical, fp, label, false, "create")
	default:
		res.Keypair, err = s.generateKeypair(ctx, q, actor, job, label, "create")
	}
	if err != nil {
		res.Destroy()
		return nil, err
	}
	if res.Status, err = s.status(ctx, q, job); err != nil {
		res.Destroy()
		return nil, err
	}
	return res, nil
}
