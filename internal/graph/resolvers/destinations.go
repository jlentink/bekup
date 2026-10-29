package resolvers

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
)

// Destination resolvers (task 2.12, D-131). Admins get the full non-secret
// configuration; maintainers get metadata only (settings stay null).
// Credentials are accepted as input and never returned: the model has only
// has* flags. Backup owners (task 3.5) will see name and type of the
// destinations their jobs use through the job type, not through these
// fields.

var (
	errDestinationsNotConfigured = errors.New("resolvers: destination service not configured")
	errDestinationNotFound       = apierror.New(apierror.NotFound, "destination not found")
)

// destinationError maps service errors to API errors.
func destinationError(err error) error {
	var inUse *destinations.InUseError
	var (
		fe *destinations.FieldError
		nr *destinations.NotReadyError
		rl *destinations.RateLimitedError
		hk *destinations.HostKeyFetchError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &nr):
		return apierror.New(apierror.Conflict, "the destination is not ready: "+nr.Reason)
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many destination checks; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.As(err, &hk):
		return apierror.New(apierror.ConnectionFailed, hk.Message)
	case errors.Is(err, destinations.ErrBusy):
		return errBusy
	case errors.Is(err, destinations.ErrNotFound):
		return errDestinationNotFound
	case errors.Is(err, destinations.ErrNameTaken):
		return apierror.New(apierror.Conflict, "another destination already uses this name")
	case errors.Is(err, destinations.ErrConflict):
		return apierror.New(apierror.Conflict, "the destination was changed in the meantime; reload and try again")
	case errors.As(err, &inUse):
		return apierror.New(apierror.Conflict, "the destination is still used by these jobs: "+strings.Join(inUse.Jobs, ", "))
	case errors.Is(err, destinations.ErrInUse):
		return apierror.New(apierror.Conflict, "the destination is still used by a job")
	case errors.Is(err, destinations.ErrWrongType):
		return apierror.New(apierror.Validation, "only SFTP destinations have host keys and SSH keys")
	default:
		return err
	}
}

// destinationCaller returns the service, the caller and its actor.
func (r *Resolver) destinationCaller(ctx context.Context) (*destinations.Service, auth.Principal, destinations.Actor, error) {
	if r.Destinations == nil {
		return nil, auth.Principal{}, destinations.Actor{}, errDestinationsNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, destinations.Actor{}, err
	}
	return r.Destinations, p, destinations.Actor{UserID: p.UserID, IP: auth.RequestMetaFrom(ctx).IP}, nil
}

func (r *Resolver) destinations(ctx context.Context) ([]model.Destination, error) {
	svc, p, _, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, destinationError(err)
	}
	out := make([]model.Destination, 0, len(list))
	for i := range list {
		out = append(out, *destinationModel(&list[i], p.Role == auth.RoleAdmin))
	}
	return out, nil
}

func (r *Resolver) destination(ctx context.Context, id uuid.UUID) (*model.Destination, error) {
	svc, p, _, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	d, err := svc.Get(ctx, id)
	if errors.Is(err, destinations.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, destinationError(err)
	}
	return destinationModel(&d, p.Role == auth.RoleAdmin), nil
}

func (r *Resolver) createDestination(ctx context.Context, in model.CreateDestinationInput) (*model.Destination, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	typ, ok := destinationTypeFromModel(in.Type)
	if !ok {
		return nil, apierror.Invalid("type", "unknown destination type")
	}
	ci := destinations.CreateInput{Name: in.Name, Type: typ, NoDelete: in.NoDelete != nil && *in.NoDelete}
	if ci.Disk, ci.S3, ci.SFTP, err = destinationInputs(in.Disk, in.S3, in.Sftp); err != nil {
		return nil, err
	}
	d, err := svc.Create(ctx, actor, ci)
	if err != nil {
		return nil, destinationError(err)
	}
	return destinationModel(&d, true), nil
}

func (r *Resolver) updateDestination(ctx context.Context, id uuid.UUID, in model.UpdateDestinationInput) (*model.Destination, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	// Setting credentials is its own operation (checked in addition to
	// destination.update, which already requires step-up).
	if (in.S3 != nil && in.S3.SecretAccessKey != nil) ||
		(in.Sftp != nil && (in.Sftp.Password != nil || in.Sftp.PrivateKey != nil || in.Sftp.PrivateKeyPassphrase != nil)) {
		if err := r.Authz.Authorize(ctx, authz.OpDestinationSecretUpdate); err != nil {
			return nil, err
		}
	}
	ui := destinations.UpdateInput{Name: in.Name, NoDelete: in.NoDelete}
	if ui.Disk, ui.S3, ui.SFTP, err = destinationInputs(in.Disk, in.S3, in.Sftp); err != nil {
		return nil, err
	}
	d, err := svc.Update(ctx, actor, id, ui)
	if err != nil {
		return nil, destinationError(err)
	}
	return destinationModel(&d, true), nil
}

func (r *Resolver) deleteDestination(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.Delete(ctx, actor, id); err != nil {
		return false, destinationError(err)
	}
	return true, nil
}

func (r *Resolver) testDestination(ctx context.Context, id uuid.UUID) (*model.DestinationTestResult, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.Test(ctx, actor, id)
	if err != nil {
		return nil, destinationError(err)
	}
	out := &model.DestinationTestResult{Ok: res.OK, ProbeRemains: res.ProbeRemains, Steps: make([]model.DestinationTestStep, 0, len(res.Steps))}
	for _, s := range res.Steps {
		out.Steps = append(out.Steps, model.DestinationTestStep{Name: s.Name, Ok: s.OK, Code: string(s.Code), Message: s.Message})
	}
	if !res.ProbeKey.IsZero() {
		k := res.ProbeKey.String()
		out.ProbeKey = &k
	}
	if res.Note != "" {
		n := res.Note
		out.Note = &n
	}
	return out, nil
}

func (r *Resolver) fetchDestinationHostKey(ctx context.Context, id uuid.UUID) (*model.SSHHostKey, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	info, err := svc.FetchHostKey(ctx, actor, id)
	if err != nil {
		return nil, destinationError(err)
	}
	host, port := info.Host, info.Port
	return &model.SSHHostKey{Type: info.Type, FingerprintSha256: info.Fingerprint, AuthorizedKey: info.AuthorizedKey, Host: &host, Port: &port}, nil
}

func (r *Resolver) confirmDestinationHostKey(ctx context.Context, id uuid.UUID, line, expectedHost string, expectedPort int) (*model.Destination, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	d, err := svc.ConfirmHostKey(ctx, actor, id, line, expectedHost, expectedPort)
	if err != nil {
		return nil, destinationError(err)
	}
	return destinationModel(&d, true), nil
}

func (r *Resolver) generateDestinationSSHKeypair(ctx context.Context, id uuid.UUID) (*model.DestinationSSHKeypair, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	kp, d, err := svc.GenerateSSHKeypair(ctx, actor, id)
	if err != nil {
		return nil, destinationError(err)
	}
	return &model.DestinationSSHKeypair{Destination: destinationModel(&d, true), PublicKey: kp.AuthorizedKey, FingerprintSha256: kp.Fingerprint}, nil
}

// --- conversions -------------------------------------------------------------

func destinationTypeFromModel(t model.DestinationType) (destinations.Type, bool) {
	switch t {
	case model.DestinationTypeDisk:
		return destinations.TypeDisk, true
	case model.DestinationTypeS3:
		return destinations.TypeS3, true
	case model.DestinationTypeSftp:
		return destinations.TypeSFTP, true
	default:
		return "", false
	}
}

func destinationTypeModel(t destinations.Type) model.DestinationType {
	switch t {
	case destinations.TypeS3:
		return model.DestinationTypeS3
	case destinations.TypeSFTP:
		return model.DestinationTypeSftp
	default:
		return model.DestinationTypeDisk
	}
}

func lockModeFromModel(m *model.ObjectLockMode) (storage.LockMode, bool) {
	if m == nil {
		return storage.LockNone, true
	}
	switch *m {
	case model.ObjectLockModeGovernance:
		return storage.LockGovernance, true
	case model.ObjectLockModeCompliance:
		return storage.LockCompliance, true
	default:
		return "", false
	}
}

func lockModeModel(m storage.LockMode) *model.ObjectLockMode {
	var v model.ObjectLockMode
	switch m {
	case storage.LockGovernance:
		v = model.ObjectLockModeGovernance
	case storage.LockCompliance:
		v = model.ObjectLockModeCompliance
	default:
		return nil
	}
	return &v
}

func sseFromModel(e *model.S3Encryption) (string, bool) {
	if e == nil {
		return string(s3store.SSENone), true
	}
	switch *e {
	case model.S3EncryptionNone:
		return string(s3store.SSENone), true
	case model.S3EncryptionSseS3:
		return string(s3store.SSES3), true
	case model.S3EncryptionSseKms:
		return string(s3store.SSEKMS), true
	default:
		return "", false
	}
}

func sseModel(s string) model.S3Encryption {
	switch s3store.SSE(s) {
	case s3store.SSES3:
		return model.S3EncryptionSseS3
	case s3store.SSEKMS:
		return model.S3EncryptionSseKms
	default:
		return model.S3EncryptionNone
	}
}

func checksumFromModel(c *model.S3ChecksumMode) (string, bool) {
	if c == nil {
		return "", true
	}
	switch *c {
	case model.S3ChecksumModeWhenSupported:
		return string(s3store.ChecksumWhenSupported), true
	case model.S3ChecksumModeWhenRequired:
		return string(s3store.ChecksumWhenRequired), true
	default:
		return "", false
	}
}

func checksumModel(s string) model.S3ChecksumMode {
	if s3store.ChecksumMode(s) == s3store.ChecksumWhenRequired {
		return model.S3ChecksumModeWhenRequired
	}
	return model.S3ChecksumModeWhenSupported
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// secretBytes converts an optional write-only input to bytes (nil when
// the field was omitted). The GraphQL layer has already decoded it into a
// Go string, which cannot be zeroed; the byte copy is zeroed by the
// service.
func secretBytes(p *string) []byte {
	if p == nil {
		return nil
	}
	return []byte(*p)
}

// destinationInputs converts the per-type inputs.
func destinationInputs(disk *model.DiskDestinationInput, s3in *model.S3DestinationInput, sftpIn *model.SftpDestinationInput) (*destinations.DiskInput, *destinations.S3Input, *destinations.SFTPInput, error) {
	var (
		d  *destinations.DiskInput
		s3 *destinations.S3Input
		sf *destinations.SFTPInput
	)
	if disk != nil {
		d = &destinations.DiskInput{Path: disk.Path}
	}
	if s3in != nil {
		mode, ok := lockModeFromModel(s3in.ObjectLockMode)
		if !ok {
			return nil, nil, nil, apierror.Invalid("s3.objectLockMode", "unknown Object Lock mode")
		}
		sse, ok := sseFromModel(s3in.Encryption)
		if !ok {
			return nil, nil, nil, apierror.Invalid("s3.encryption", "unknown encryption mode")
		}
		cs, ok := checksumFromModel(s3in.ChecksumMode)
		if !ok {
			return nil, nil, nil, apierror.Invalid("s3.checksumMode", "unknown checksum mode")
		}
		days := 0
		if s3in.ObjectLockRetentionDays != nil {
			days = *s3in.ObjectLockRetentionDays
		}
		s3 = &destinations.S3Input{
			Endpoint: derefString(s3in.Endpoint), Region: s3in.Region, Bucket: s3in.Bucket, Prefix: derefString(s3in.Prefix),
			PathStyle: s3in.PathStyle != nil && *s3in.PathStyle, SSE: sse, KMSKeyID: derefString(s3in.KmsKeyID),
			StorageClass: derefString(s3in.StorageClass), Checksum: cs, ConditionalWrites: s3in.ConditionalWrites,
			ObjectLockMode: mode, ObjectLockRetentionDays: days, AccessKeyID: s3in.AccessKeyID,
			SecretAccessKey: secretBytes(s3in.SecretAccessKey), CABundlePEM: derefString(s3in.CaBundlePem),
		}
	}
	if sftpIn != nil {
		port := 0
		if sftpIn.Port != nil {
			port = *sftpIn.Port
		}
		sf = &destinations.SFTPInput{
			Host: sftpIn.Host, Port: port, User: sftpIn.User, BasePath: sftpIn.BasePath, Dir: derefString(sftpIn.Dir),
			Password: secretBytes(sftpIn.Password), PrivateKey: secretBytes(sftpIn.PrivateKey),
			Passphrase: secretBytes(sftpIn.PrivateKeyPassphrase),
		}
	}
	return d, s3, sf, nil
}

// destinationModel converts d; full adds the non-secret settings (admins).
func destinationModel(d *destinations.Destination, full bool) *model.Destination {
	m := &model.Destination{
		ID: d.ID, Name: d.Name, Type: destinationTypeModel(d.Type), NoDelete: d.NoDelete,
		ObjectLockMode: lockModeModel(d.ObjectLockMode), Ready: d.Ready,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
	if lt := d.LastTest; lt != nil {
		m.LastTest = &model.DestinationLastTest{At: lt.At, Ok: lt.OK, Code: nonEmpty(lt.Code), Stale: lt.Stale}
	}
	if m.ObjectLockMode != nil {
		days := d.ObjectLockRetentionDays
		m.ObjectLockRetentionDays = &days
	}
	if d.NotReadyReason != "" {
		reason := d.NotReadyReason
		m.NotReadyReason = &reason
	}
	if !full {
		return m
	}
	set := &model.DestinationSettings{}
	switch {
	case d.Disk != nil:
		set.Disk = &model.DiskDestinationSettings{Path: d.Disk.Path}
	case d.S3 != nil:
		c := d.S3
		s := &model.S3DestinationSettings{
			Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle,
			Encryption: sseModel(c.SSE), ChecksumMode: checksumModel(c.Checksum),
			ConditionalWrites: c.ConditionalWrites, AccessKeyID: c.AccessKeyID, HasSecretAccessKey: d.HasSecretAccessKey,
		}
		s.Endpoint = nonEmpty(c.Endpoint)
		s.KmsKeyID = nonEmpty(c.KMSKeyID)
		s.StorageClass = nonEmpty(c.StorageClass)
		s.CaBundlePem = nonEmpty(c.CABundlePEM)
		set.S3 = s
	case d.SFTP != nil:
		c := d.SFTP
		s := &model.SftpDestinationSettings{
			Host: c.Host, Port: c.Port, User: c.User, BasePath: c.BasePath, Dir: c.Dir,
			AuthMethod:  model.SftpAuthMethodNone,
			HasPassword: d.HasPassword, HasPrivateKey: d.HasPrivateKey, HasPassphrase: d.HasPassphrase,
			PublicKey: nonEmpty(c.PublicKey),
		}
		switch {
		case d.HasPassword:
			s.AuthMethod = model.SftpAuthMethodPassword
		case d.HasPrivateKey:
			s.AuthMethod = model.SftpAuthMethodPrivateKey
		}
		if d.HostKey != nil {
			at := d.HostKey.ConfirmedAt
			s.HostKey = &model.SSHHostKey{
				Type: d.HostKey.Type, FingerprintSha256: d.HostKey.Fingerprint,
				AuthorizedKey: d.HostKey.AuthorizedKey, ConfirmedAt: &at,
			}
		}
		set.Sftp = s
	}
	m.Settings = set
	return m
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// destinationUsage serves Query.destinationUsage (staff, D-196).
func (r *Resolver) destinationUsage(ctx context.Context) ([]model.DestinationUsage, error) {
	svc, _, _, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpStorageUsageView); err != nil {
		return nil, err
	}
	list, err := svc.Usage(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.DestinationUsage, 0, len(list))
	for _, u := range list {
		out = append(out, model.DestinationUsage{
			DestinationID: u.ID, Name: u.Name, Type: destinationTypeModel(u.Type),
			StoredBytes: float64(u.StoredBytes), ArtifactCount: int(min(u.ArtifactCount, math.MaxInt32)),
		})
	}
	return out, nil
}
