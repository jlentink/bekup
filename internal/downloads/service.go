package downloads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage"
)

// Mode is what a download delivers.
type Mode string

// Download modes.
const (
	// ModeRaw is the stored artifact, unchanged (compressed, normally
	// encrypted).
	ModeRaw Mode = "raw"
	// ModePlain is the decrypted, decompressed dump (.dump for
	// pg_restore, .sql for mysql/psql).
	ModePlain Mode = "plain"
)

// Limits (D-150).
const (
	// TicketTTL is the lifetime of a ticket.
	TicketTTL = 60 * time.Second
	// MaxActiveTicketsPerUser bounds unused, unexpired tickets per user.
	MaxActiveTicketsPerUser = 5
	// DefaultMaxConcurrent and DefaultMaxConcurrentPerUser bound running
	// downloads per process (a passphrase decryption needs up to 256 MiB
	// for scrypt).
	DefaultMaxConcurrent        = 4
	DefaultMaxConcurrentPerUser = 2
	// MaxIdentityBytes bounds the age identity input.
	MaxIdentityBytes = 4096
	// URLPrefix is the path of the download route; the access log
	// redacts everything after it (httpserver.DefaultRedactedPathPrefixes).
	URLPrefix = "/api/download/"
	// cleanupGrace keeps expired rows a little longer than their
	// lifetime (diagnostics of a "ticket expired" answer).
	cleanupGrace = time.Minute
)

// Encryption modes stored on artifacts (pipeline.EncryptionMode values).
const (
	encPassphrase = "passphrase"
	encRecipients = "recipients"
	encNone       = "none"
)

// Errors of CreateTicket (the GraphQL layer maps them).
var (
	// ErrNotFound: no such artifact.
	ErrNotFound = errors.New("downloads: artifact not found")
	// ErrUnavailable: the artifact was deleted or is missing.
	ErrUnavailable = errors.New("downloads: the artifact is no longer available")
	// ErrUnverified: the dump's native verification did not pass.
	ErrUnverified = errors.New("downloads: the artifact is not verified")
	// ErrUnsigned: no signed manifest covers the artifact on its
	// destination.
	ErrUnsigned = errors.New("downloads: the artifact is not covered by a signed manifest")
	// ErrKeyUnavailable: the passphrase version of the artifact is gone.
	ErrKeyUnavailable = errors.New("downloads: the key of this artifact is not available")
)

// DB is the app database.
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Authorizer is the central policy (*authz.Policy).
type Authorizer interface {
	Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error
}

// DestinationOpener opens a destination's storage backend
// (*destinations.Service).
type DestinationOpener interface {
	Open(ctx context.Context, id uuid.UUID) (storage.Destination, error)
}

// KeySource reads job keys by version (*jobkeys.Service).
type KeySource interface {
	PassphraseForVersion(ctx context.Context, job uuid.UUID, version int) (*crypto.Secret, error)
	RecipientForVersion(ctx context.Context, job uuid.UUID, version int) (jobkeys.RecipientInfo, error)
}

// Options configure New.
type Options struct {
	DB           DB
	Keyring      *crypto.Keyring
	Auditor      *audit.Auditor
	Authorizer   Authorizer
	Destinations DestinationOpener
	Keys         KeySource
	// MaxConcurrent / MaxConcurrentPerUser bound running downloads in
	// this process (0 = the defaults).
	MaxConcurrent        int
	MaxConcurrentPerUser int
	// HoldTTL is the lifetime of a download's artifact hold between
	// renewals (0 = runstate.DefaultHoldTTL; renewed every third of it).
	HoldTTL time.Duration
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Service creates and serves download tickets.
type Service struct {
	o   Options
	lim *limiter
}

// New returns the service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Authorizer == nil || o.Destinations == nil ||
		o.Keys == nil || o.Logger == nil {
		return nil, errors.New("downloads: DB, Keyring, Auditor, Authorizer, Destinations, Keys and Logger are required")
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = DefaultMaxConcurrent
	}
	if o.MaxConcurrentPerUser <= 0 {
		o.MaxConcurrentPerUser = DefaultMaxConcurrentPerUser
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HoldTTL <= 0 {
		o.HoldTTL = runstate.DefaultHoldTTL
	}
	o.HoldTTL = min(max(o.HoldTTL, 3*time.Second), runstate.MaxHoldTTL)
	return &Service{o: o, lim: newLimiter(o.MaxConcurrent, o.MaxConcurrentPerUser)}, nil
}

// CreateInput is a ticket request. Identity (age identity-file content)
// is borrowed: the caller zeroes it.
type CreateInput struct {
	ArtifactID uuid.UUID
	Mode       Mode
	Identity   []byte
}

// Ticket is a created ticket. URL holds the ticket value: return it to
// the caller only, never log it.
type Ticket struct {
	ID        uuid.UUID
	URL       string
	ExpiresAt time.Time
	Mode      Mode
	// FileName is the name the download is saved under.
	FileName string
	// Format is "dump" (pg_restore) or "sql" (psql/mysql).
	Format string
	// RestoreCommand is a shell command template for the downloaded file
	// (placeholders in angle brackets, never values from the database).
	RestoreCommand string
}

// String never prints the URL.
func (t Ticket) String() string { return "Ticket{" + t.ID.String() + "}" }

// artifactInfo is an artifact with the facts a download needs.
type artifactInfo struct {
	row       sqlc.GetDownloadArtifactRow
	plainExt  string
	storedExt string
}

func (a *artifactInfo) art() *sqlc.Artifact { return &a.row.Artifact }

func (a *artifactInfo) encryption() string {
	if a.row.Artifact.EncryptionMode == nil {
		return ""
	}
	return *a.row.Artifact.EncryptionMode
}

func (a *artifactInfo) dbName() string {
	if a.row.Artifact.Kind == sqlc.ArtifactKindGlobals {
		return "globals"
	}
	if a.row.ItemDbName != nil {
		return *a.row.ItemDbName
	}
	return "database"
}

// fileName is the name of the download in mode m.
func (a *artifactInfo) fileName(m Mode) string {
	ext := a.storedExt
	if m == ModePlain {
		ext = a.plainExt
	}
	return downloadName(a.row.JobSlug, a.dbName(), *a.row.RunStorageTimestamp, ext)
}

// loadArtifact reads an artifact and checks that it can be downloaded:
// a database or globals object, present, verified and signed.
func loadArtifact(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (*artifactInfo, error) {
	row, err := q.GetDownloadArtifact(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("downloads: load artifact: %w", err)
	}
	return &artifactInfo{row: row}, nil
}

// check applies the download preconditions (after authorization).
func (a *artifactInfo) check() error {
	art := a.art()
	switch {
	case art.Kind == sqlc.ArtifactKindManifest:
		return serviceutil.FieldErr("artifactId", "manifest files are not downloadable here")
	case art.State != sqlc.ArtifactStatePresent:
		return ErrUnavailable
	case !art.Verified:
		return ErrUnverified
	case art.SigningKeyID == nil:
		return ErrUnsigned
	case a.row.RunStorageTimestamp == nil:
		return ErrUnavailable
	}
	switch a.encryption() {
	case encPassphrase, encRecipients, encNone:
	default:
		return fmt.Errorf("downloads: artifact %s has an unknown encryption mode", art.ID)
	}
	var err error
	a.plainExt, a.storedExt, err = storedFormat(art.ObjectKey, a.encryption() != encNone)
	if err != nil {
		return fmt.Errorf("downloads: artifact %s: %w", art.ID, err)
	}
	return nil
}

// CreateTicket authorizes the caller (artifact.download: admin, or the
// owner of the artifact's job with role user; fresh step-up), checks the
// artifact and the identity, stores the ticket hash (and the sealed
// identity) and audits download.ticket_created in one transaction.
func (s *Service) CreateTicket(ctx context.Context, in CreateInput) (Ticket, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return Ticket{}, s.o.Authorizer.Authorize(ctx, authz.OpArtifactDownload)
	}
	if in.Mode != ModeRaw && in.Mode != ModePlain {
		return Ticket{}, serviceutil.FieldErr("mode", "unknown download mode")
	}
	q := sqlc.New(s.o.DB)
	a, err := loadArtifact(ctx, q, in.ArtifactID)
	if errors.Is(err, ErrNotFound) {
		// Callers who could not download anyway learn nothing about
		// whether the id exists (owners need a real job).
		if aerr := s.o.Authorizer.Authorize(ctx, authz.OpArtifactDownload, authz.Job{ID: uuid.Nil}); aerr != nil {
			return Ticket{}, aerr
		}
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if err := s.o.Authorizer.Authorize(ctx, authz.OpArtifactDownload, authz.Job{ID: a.art().JobID}); err != nil {
		return Ticket{}, err
	}
	if err := a.check(); err != nil {
		return Ticket{}, err
	}
	needSeal, err := s.checkKeys(ctx, a, in)
	if err != nil {
		return Ticket{}, err
	}
	value, hash := newTicket()
	id := uuid.New()
	var sealedIdentity []byte
	if needSeal {
		sealedIdentity, err = s.sealIdentity(id, in.Identity)
		if err != nil {
			return Ticket{}, err
		}
	}
	now := s.o.Now().UTC().Truncate(time.Microsecond)
	art := a.art()
	t := Ticket{
		ID: id, URL: URLPrefix + value, ExpiresAt: now.Add(TicketTTL), Mode: in.Mode,
		FileName: a.fileName(in.Mode), Format: a.plainExt,
	}
	t.RestoreCommand = restoreCommand(a, in.Mode, t.FileName)
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		n, err := q.CountActiveDownloadTickets(ctx, sqlc.CountActiveDownloadTicketsParams{UserID: pr.UserID, Now: now})
		if err != nil {
			return fmt.Errorf("downloads: count tickets: %w", err)
		}
		if n >= MaxActiveTicketsPerUser {
			return &serviceutil.RateLimitedError{RetryAfter: TicketTTL}
		}
		if _, err := q.InsertDownloadTicket(ctx, sqlc.InsertDownloadTicketParams{
			ID: id, TicketHash: hash, SessionID: pr.SessionID, UserID: pr.UserID, ArtifactID: art.ID,
			Mode: sqlc.DownloadMode(in.Mode), SealedIdentity: sealedIdentity, CreatedAt: now, ExpiresAt: t.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("downloads: insert ticket: %w", err)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(pr.UserID), Action: audit.ActionDownloadTicketCreated,
			TargetType: "artifact", TargetID: art.ID.String(), IP: auth.RequestMetaFrom(ctx).IP,
			Details: s.details(a, id, in.Mode),
		})
		return err
	})
	crypto.Zero(sealedIdentity)
	if err != nil {
		return Ticket{}, err
	}
	return t, nil
}

// details are the audit details of a download (never the ticket value or
// key material).
func (s *Service) details(a *artifactInfo, ticket uuid.UUID, m Mode) audit.Details {
	art := a.art()
	return audit.Details{
		"ticket_id": ticket.String(), "mode": string(m), "job_id": art.JobID.String(), "run_id": art.RunID.String(),
		"destination_id": art.DestinationID.String(), "encryption": a.encryption(), "kind": string(art.Kind),
	}
}

// checkKeys validates the key inputs of a ticket. It reports whether
// in.Identity must be sealed into the ticket (a plain download of a
// recipient-encrypted artifact with a matching identity).
func (s *Service) checkKeys(ctx context.Context, a *artifactInfo, in CreateInput) (bool, error) {
	art := a.art()
	needIdentity := in.Mode == ModePlain && a.encryption() == encRecipients
	if !needIdentity {
		if len(in.Identity) > 0 {
			return false, serviceutil.FieldErr("ageIdentity", "an identity is only needed for plain downloads of recipient-encrypted backups")
		}
		if in.Mode == ModePlain && a.encryption() == encPassphrase {
			if art.PassphraseVersion == nil {
				return false, ErrKeyUnavailable
			}
			p, err := s.o.Keys.PassphraseForVersion(ctx, art.JobID, int(*art.PassphraseVersion))
			if errors.Is(err, jobkeys.ErrVersionNotFound) {
				return false, ErrKeyUnavailable
			}
			if err != nil {
				return false, err
			}
			p.Destroy()
		}
		return false, nil
	}
	if len(in.Identity) == 0 {
		return false, serviceutil.FieldErr("ageIdentity", "this backup is encrypted to an age recipient: paste its private key (AGE-SECRET-KEY-1...)")
	}
	if len(in.Identity) > MaxIdentityBytes {
		return false, serviceutil.FieldErr("ageIdentity", "the identity is too long")
	}
	if art.RecipientVersion == nil {
		return false, ErrKeyUnavailable
	}
	info, err := s.o.Keys.RecipientForVersion(ctx, art.JobID, int(*art.RecipientVersion))
	if errors.Is(err, jobkeys.ErrVersionNotFound) {
		return false, ErrKeyUnavailable
	}
	if err != nil {
		return false, err
	}
	ids, err := crypto.ParseAgeIdentities(in.Identity)
	if err != nil {
		// Never echo the input.
		return false, serviceutil.FieldErr("ageIdentity", "not an age X25519 identity (AGE-SECRET-KEY-1...)")
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok && x.Recipient().String() == info.Recipient {
			return true, nil
		}
	}
	return false, serviceutil.FieldErr("ageIdentity", "this key does not match the recipient of the backup")
}

// identityAAD binds a sealed identity to its ticket row.
func identityAAD(id uuid.UUID) crypto.AAD {
	return crypto.AAD{Table: "download_tickets", RowID: id.String(), Field: "identity"}
}

func (s *Service) sealIdentity(id uuid.UUID, identity []byte) ([]byte, error) {
	sealed, err := s.o.Keyring.Seal(identity, identityAAD(id))
	if err != nil {
		return nil, fmt.Errorf("downloads: seal identity: %w", err)
	}
	return sealed, nil
}

// restoreCommand is the shell command template shown with a download.
func restoreCommand(a *artifactInfo, m Mode, file string) string {
	if m == ModeRaw {
		var c string
		switch a.encryption() {
		case encPassphrase:
			c = "age --decrypt " + file + " | "
		case encRecipients:
			c = "age --decrypt --identity <key-file> " + file + " | "
		default:
			c = "cat " + file + " | "
		}
		return c + "zstd --decompress --stdout > <dump-file>"
	}
	switch {
	case a.plainExt == "dump":
		return "pg_restore --exit-on-error --no-owner --dbname <database> " + file
	case a.row.ConnectionEngine == sqlc.ConnectionEnginePostgres:
		return "psql --set ON_ERROR_STOP=1 --dbname <database> --file " + file
	default:
		return "mysql <database> < " + file
	}
}

// DeleteExpired removes tickets that expired more than a minute ago
// (housekeeping; also removes any identity of an unused one) and artifact
// holds that expired (they protect nothing any more).
func (s *Service) DeleteExpired(ctx context.Context) (int64, error) {
	n, err := sqlc.New(s.o.DB).DeleteExpiredDownloadTickets(ctx, s.o.Now().Add(-cleanupGrace))
	if err != nil {
		return 0, fmt.Errorf("downloads: delete expired tickets: %w", err)
	}
	if _, err := runstate.DeleteExpiredHolds(ctx, s.o.DB, time.Now().Add(-cleanupGrace)); err != nil {
		return n, err
	}
	return n, nil
}

// cleanupInterval is the housekeeping period of RunCleanup.
const cleanupInterval = 30 * time.Second

// RunCleanup deletes expired tickets every 30 seconds until ctx ends.
// Failures are logged and retried at the next tick.
func (s *Service) RunCleanup(ctx context.Context) error {
	t := time.NewTicker(cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			n, err := s.DeleteExpired(ctx)
			if err != nil {
				s.o.Logger.WarnContext(ctx, "download ticket cleanup failed", slog.Any("error", err))
				continue
			}
			if n > 0 {
				s.o.Logger.DebugContext(ctx, "expired download tickets deleted", slog.Int64("count", n))
			}
		}
	}
}
