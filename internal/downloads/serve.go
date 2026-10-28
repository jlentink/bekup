package downloads

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// Rejection reasons of download.ticket_rejected.
const (
	reasonReused  = "reused"
	reasonExpired = "expired"
	reasonSession = "session_mismatch"
	reasonDenied  = "forbidden"
)

// Failure codes of download.failed (fixed texts; never backend details).
const (
	failUnavailable = "artifact_unavailable"
	failKey         = "key_unavailable"
	failDestination = "destination_unavailable"
	failManifest    = "manifest_invalid"
	failIntegrity   = "integrity_mismatch"
	failDecrypt     = "decrypt_failed"
	failClient      = "client_aborted"
	failHold        = "artifact_hold_lost"
	failInternal    = "internal"
)

// redemption is a redeemed ticket.
type redemption struct {
	ticket   sqlc.DownloadTicket
	artifact *artifactInfo
	identity []byte // opened age identity (zero after use)
	// hold keeps retention from deleting the artifact while it streams
	// (nil: the artifact was no longer present at redemption).
	hold *runstate.Hold
}

// rejection is a refused ticket.
type rejection struct {
	status int
}

func (r *rejection) Error() string { return "downloads: ticket rejected" }

// ServeDownload serves GET /api/download/{ticket} (the session middleware
// has run). It never logs the ticket value.
func (s *Service) ServeDownload(w http.ResponseWriter, r *http.Request, value string) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed)
		return
	}
	// Only top-level same-origin navigations (or a pasted link): a
	// cross-site page cannot make the browser redeem a ticket.
	if sfs := r.Header.Values("Sec-Fetch-Site"); len(sfs) > 1 || (len(sfs) == 1 && sfs[0] != "same-origin" && sfs[0] != "none") {
		writeStatus(w, http.StatusNotFound)
		return
	}
	hash, ok := hashTicket(value)
	if !ok {
		writeStatus(w, http.StatusNotFound)
		return
	}
	pr, authed := auth.FromContext(ctx)
	if authed {
		release, ok := s.lim.acquire(pr.UserID)
		if !ok {
			w.Header().Set("Retry-After", "5")
			writeStatus(w, http.StatusTooManyRequests)
			return
		}
		defer release()
	}
	red, err := s.redeem(ctx, hash, pr, authed)
	if err != nil {
		var rej *rejection
		if errors.As(err, &rej) {
			writeStatus(w, rej.status)
			return
		}
		s.o.Logger.ErrorContext(ctx, "redeeming a download ticket failed", slog.Any("error", err),
			slog.String("request_id", httpserver.RequestID(ctx)))
		writeStatus(w, http.StatusInternalServerError)
		return
	}
	defer crypto.Zero(red.identity)
	sctx, stop := s.keepHold(ctx, red)
	defer stop()
	s.stream(sctx, w, red, pr)
}

// keepHold renews the redemption's artifact hold while the download streams
// and releases it afterwards (stop). A hold that expired before it could be
// renewed cancels the stream (retention may delete the object now).
func (s *Service) keepHold(ctx context.Context, red *redemption) (context.Context, func()) {
	if red.hold == nil {
		return ctx, func() {}
	}
	sctx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(s.o.HoldTTL / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-sctx.Done():
				return
			case <-t.C:
				err := runstate.RenewHold(sctx, s.o.DB, red.hold.ID, s.o.HoldTTL)
				switch {
				case errors.Is(err, runstate.ErrHoldLost):
					s.o.Logger.ErrorContext(sctx, "download hold expired; stopping the download",
						slog.String("artifact_id", red.ticket.ArtifactID.String()))
					cancel(runstate.ErrHoldLost)
					return
				case err != nil && sctx.Err() == nil:
					s.o.Logger.WarnContext(sctx, "renewing the download hold failed", slog.Any("error", err))
				}
			}
		}
	}()
	return sctx, func() {
		close(done)
		<-finished
		cancel(nil)
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer rcancel()
		if err := runstate.ReleaseHold(rctx, s.o.DB, red.hold.ID); err != nil {
			// It expires on its own.
			s.o.Logger.WarnContext(rctx, "releasing the download hold failed", slog.Any("error", err))
		}
	}
}

// redeem looks the ticket up by its hash and, in one transaction, either
// marks it used (clearing its identity) or rejects it: used (410), expired
// (410, burned), another or no session (404, burned) or a caller who lost
// access (404, burned). Rejections of a known ticket are audited.
func (s *Service) redeem(ctx context.Context, hash []byte, pr auth.Principal, authed bool) (*redemption, error) {
	var red *redemption
	var rej *rejection
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		t, err := q.GetDownloadTicketForUpdate(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			rej = &rejection{status: http.StatusNotFound}
			return nil
		}
		if err != nil {
			return fmt.Errorf("downloads: load ticket: %w", err)
		}
		defer crypto.Zero(t.SealedIdentity)
		now := s.o.Now()
		reason, status, burn := "", 0, false
		switch {
		case t.UsedAt != nil:
			reason, status = reasonReused, http.StatusGone
		case !now.Before(t.ExpiresAt):
			reason, status, burn = reasonExpired, http.StatusGone, true
		case !authed || subtle.ConstantTimeCompare(pr.SessionID[:], t.SessionID[:]) != 1 || pr.UserID != t.UserID:
			reason, status, burn = reasonSession, http.StatusNotFound, true
		}
		var a *artifactInfo
		if reason == "" {
			if a, err = loadArtifact(ctx, q, t.ArtifactID); err != nil {
				return err
			}
			if err := s.o.Authorizer.Authorize(ctx, authz.OpArtifactDownloadRedeem, authz.Job{ID: a.art().JobID}); err != nil {
				if !isDenial(err) {
					return err
				}
				reason, status, burn = reasonDenied, http.StatusNotFound, true
			}
		}
		if burn || reason == "" {
			if _, err := q.RedeemDownloadTicket(ctx, sqlc.RedeemDownloadTicketParams{ID: t.ID, UsedAt: now}); err != nil {
				return fmt.Errorf("downloads: redeem ticket: %w", err)
			}
		}
		if reason != "" {
			rej = &rejection{status: status}
			actor := audit.SystemActor()
			if authed {
				actor = audit.UserActor(pr.UserID)
			}
			_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
				Actor: actor, Action: audit.ActionDownloadTicketRejected, TargetType: "artifact",
				TargetID: t.ArtifactID.String(), IP: auth.RequestMetaFrom(ctx).IP,
				Details: audit.Details{
					"ticket_id": t.ID.String(), "reason": reason, "mode": string(t.Mode),
					"ticket_user_id": t.UserID.String(), "anonymous": !authed,
				},
			})
			return err
		}
		red = &redemption{ticket: t, artifact: a}
		// Hold the artifact for the stream (task 3.11): retention skips a
		// held artifact, and the hold is taken under a share lock of the
		// artifact row, so it cannot interleave with a deletion.
		hold, ok, err := runstate.AcquireHoldTx(ctx, q, t.ArtifactID, runstate.HoldDownload, t.ID, s.o.HoldTTL)
		if err != nil {
			return err
		}
		if ok {
			red.hold = &hold
		}
		if len(t.SealedIdentity) > 0 {
			id, err := s.o.Keyring.Open(t.SealedIdentity, identityAAD(t.ID))
			if err != nil {
				return fmt.Errorf("downloads: open identity of ticket %s: %w", t.ID, err)
			}
			red.identity = id
		}
		return nil
	})
	if err != nil {
		if red != nil {
			crypto.Zero(red.identity)
		}
		return nil, err
	}
	if rej != nil {
		return nil, rej
	}
	return red, nil
}

// isDenial reports a policy denial (as opposed to a failed lookup).
func isDenial(err error) bool {
	var ae *apierror.Error
	return errors.As(err, &ae)
}

// streamFailure is a failed download: the audit code and, before the
// response started, the status to answer.
type streamFailure struct {
	code   string
	status int
	err    error
}

func fail(code string, status int, err error) *streamFailure {
	return &streamFailure{code: code, status: status, err: err}
}

// stream serves a redeemed ticket. Every outcome is audited
// (download.started, then download.completed or download.failed).
func (s *Service) stream(ctx context.Context, w http.ResponseWriter, red *redemption, pr auth.Principal) {
	a := red.artifact
	mode := Mode(red.ticket.Mode)
	details := s.details(a, red.ticket.ID, mode)
	hw := newHoldbackWriter(w)
	defer hw.wipe()
	started := false
	f := s.prepareAndCopy(ctx, w, hw, red, mode, func() error {
		_, err := s.o.Auditor.Record(ctx, audit.Event{
			Actor: audit.UserActor(pr.UserID), Action: audit.ActionDownloadStarted, TargetType: "artifact",
			TargetID: a.art().ID.String(), IP: auth.RequestMetaFrom(ctx).IP, Details: details,
		})
		started = err == nil
		return err
	})
	actx := context.WithoutCancel(ctx)
	ev := audit.Event{
		Actor: audit.UserActor(pr.UserID), Action: audit.ActionDownloadCompleted, TargetType: "artifact",
		TargetID: a.art().ID.String(), IP: auth.RequestMetaFrom(ctx).IP, Details: details,
	}
	if f != nil {
		ev.Action = audit.ActionDownloadFailed
		ev.Details["reason"] = f.code
		if f.code != failClient {
			s.o.Logger.ErrorContext(actx, "download failed", slog.String("reason", f.code), slog.Any("error", f.err),
				slog.String("artifact_id", a.art().ID.String()), slog.String("request_id", httpserver.RequestID(ctx)))
		}
	}
	if started || f != nil {
		if _, err := s.o.Auditor.Record(actx, ev); err != nil {
			s.o.Logger.ErrorContext(actx, "recording the download outcome failed", slog.Any("error", err))
		}
	}
	if f == nil {
		return
	}
	if hw.started {
		// Headers and part of the body are out: abort the connection so
		// the client sees an incomplete download, never a complete file.
		panic(http.ErrAbortHandler)
	}
	writeStatus(w, f.status)
}

// prepareAndCopy checks the artifact, verifies the manifest, opens the
// object and streams it. onStart records download.started right before
// the headers are set (failing it fails the download).
func (s *Service) prepareAndCopy(ctx context.Context, w http.ResponseWriter, hw *holdbackWriter, red *redemption,
	mode Mode, onStart func() error,
) *streamFailure {
	a := red.artifact
	if red.hold == nil {
		return fail(failUnavailable, http.StatusGone, ErrUnavailable)
	}
	if err := a.check(); err != nil {
		return fail(failUnavailable, http.StatusGone, err)
	}
	art := a.art()
	var dec pipeline.Decryption
	if mode == ModePlain {
		switch a.encryption() {
		case encPassphrase:
			if art.PassphraseVersion == nil {
				return fail(failKey, http.StatusInternalServerError, ErrKeyUnavailable)
			}
			p, err := s.o.Keys.PassphraseForVersion(ctx, art.JobID, int(*art.PassphraseVersion))
			if err != nil {
				return fail(failKey, http.StatusInternalServerError, err)
			}
			defer p.Destroy()
			dec.Passphrase = p.Bytes()
		case encRecipients:
			if len(red.identity) == 0 {
				return fail(failKey, http.StatusInternalServerError, errors.New("downloads: ticket has no identity"))
			}
			dec.Identities = red.identity
		default:
			dec.None = true
		}
	}
	dest, err := s.o.Destinations.Open(ctx, art.DestinationID)
	if err != nil {
		return fail(failDestination, http.StatusBadGateway, err)
	}
	defer func() {
		if cerr := dest.Close(); cerr != nil {
			s.o.Logger.WarnContext(ctx, "closing the destination failed", slog.Any("error", cerr))
		}
	}()
	if f := s.verifyManifest(ctx, dest, a); f != nil {
		return f
	}
	key, err := storage.ParseKey(art.ObjectKey)
	if err != nil {
		return fail(failUnavailable, http.StatusInternalServerError, err)
	}
	rc, info, err := dest.Get(ctx, key)
	if err != nil {
		return fail(failDestination, http.StatusBadGateway, err)
	}
	defer s.closeLogged(ctx, rc, "artifact reader")
	if info.Size > 0 && info.Size != art.Size || len(info.SHA256) > 0 && !bytes.Equal(info.SHA256, art.Sha256) {
		return fail(failIntegrity, http.StatusBadGateway, errors.New("downloads: stored object differs from the record"))
	}
	stored := artifactverify.NewDigestReader(rc, art.Size)
	var body io.Reader = stored
	var raw *artifactverify.DigestReader
	if mode == ModePlain {
		plain, err := pipeline.OpenArtifact(stored, dec)
		if err != nil {
			return fail(failDecrypt, http.StatusInternalServerError, err)
		}
		defer s.closeLogged(ctx, plain, "artifact decoder")
		limit := int64(math.MaxInt64)
		if a.row.ItemRawSize != nil && *a.row.ItemRawSize > 0 {
			limit = *a.row.ItemRawSize
		}
		raw = artifactverify.NewDigestReader(plain, limit)
		body = raw
	}
	if err := onStart(); err != nil {
		return fail(failInternal, http.StatusInternalServerError, err)
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", ContentDisposition(a.fileName(mode)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if mode == ModeRaw {
		h.Set("Content-Length", strconv.FormatInt(art.Size, 10))
	}
	if err := httpserver.LiftDeadlines(w); err != nil {
		return fail(failInternal, http.StatusInternalServerError, err)
	}
	if _, err := io.Copy(hw, body); err != nil {
		return copyFailure(ctx, err)
	}
	if raw != nil {
		// The dump ended: the rest of the stored object (age's final
		// chunk, zstd frame end) still counts for the stored digest.
		if _, err := io.Copy(io.Discard, stored); err != nil {
			return copyFailure(ctx, err)
		}
		if a.row.ItemRawSize != nil && raw.N() != *a.row.ItemRawSize ||
			len(a.row.ItemRawSha256) > 0 && !bytes.Equal(raw.Sum(), a.row.ItemRawSha256) {
			return fail(failIntegrity, http.StatusBadGateway, errors.New("downloads: dump digest mismatch"))
		}
	}
	if !stored.Matches(art.Size, art.Sha256) {
		return fail(failIntegrity, http.StatusBadGateway, errors.New("downloads: artifact digest mismatch"))
	}
	if err := hw.finish(); err != nil {
		return fail(failClient, http.StatusInternalServerError, err)
	}
	return nil
}

// copyFailure classifies an error of the copy loop.
func copyFailure(ctx context.Context, err error) *streamFailure {
	switch {
	case errors.Is(context.Cause(ctx), runstate.ErrHoldLost):
		return fail(failHold, http.StatusInternalServerError, err)
	case ctx.Err() != nil:
		return fail(failClient, http.StatusInternalServerError, err)
	case errors.Is(err, artifactverify.ErrTooLong):
		return fail(failIntegrity, http.StatusBadGateway, err)
	case errors.Is(err, pipeline.ErrArtifact):
		return fail(failDecrypt, http.StatusBadGateway, err)
	default:
		// A read error of the destination, the decryption (an age chunk
		// that fails authentication) or the decompression, or a write
		// error towards the client.
		return fail(failIntegrity, http.StatusBadGateway, err)
	}
}

// verifyManifest verifies the run's signed manifest.json on the artifact's
// destination against the database records (artifactverify.VerifyManifest),
// so the recorded digest the stream is checked against is the one bekup
// signed.
func (s *Service) verifyManifest(ctx context.Context, dest storage.Destination, a *artifactInfo) *streamFailure {
	_, err := artifactverify.VerifyManifest(ctx, sqlc.New(s.o.DB), dest, artifactverify.FromDownloadRow(&a.row))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, artifactverify.ErrDestination):
		return fail(failDestination, http.StatusBadGateway, err)
	case errors.Is(err, artifactverify.ErrManifest):
		return fail(failManifest, http.StatusBadGateway, err)
	default:
		return fail(failManifest, http.StatusInternalServerError, err)
	}
}

// writeStatus writes a short, generic plain-text error (no details, no
// download headers).
func writeStatus(w http.ResponseWriter, status int) {
	h := w.Header()
	h.Del("Content-Disposition")
	h.Del("Content-Length")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, http.StatusText(status)+"\n"); err != nil {
		// The client went away; there is nothing left to tell it.
		return
	}
}

// closeLogged closes c and logs a failure (reads are finished or
// abandoned at this point; a close error changes nothing for the client).
func (s *Service) closeLogged(ctx context.Context, c io.Closer, what string) {
	if err := c.Close(); err != nil {
		s.o.Logger.DebugContext(ctx, "closing a download stream failed", slog.String("stream", what), slog.Any("error", err))
	}
}
