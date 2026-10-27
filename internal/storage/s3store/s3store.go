package s3store

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// metaSHA256 is the user-metadata key holding the hex SHA-256 of the object
// (x-amz-meta-bekup-sha256).
const metaSHA256 = "bekup-sha256"

// abortTimeout bounds the cleanup of a failed multipart upload. It runs on
// a context detached from the caller's cancellation.
const abortTimeout = 30 * time.Second

// completeTimeout bounds CompleteMultipartUpload, which runs detached from
// the caller's cancellation once every part is stored (S3 may take minutes
// to assemble large objects).
const completeTimeout = 10 * time.Minute

// staticProvider serves the configured static key pair. It is the only
// credentials source: the client is built without the SDK's default chain
// (no environment, shared files, container or instance metadata).
type staticProvider struct {
	id     string
	secret *crypto.Secret
}

// Retrieve implements aws.CredentialsProvider. The SDK needs a string, so
// a copy of the key lives in the SDK's credentials cache while the
// destination is open (strings cannot be zeroed).
func (p staticProvider) Retrieve(context.Context) (aws.Credentials, error) {
	if p.secret.Len() == 0 {
		return aws.Credentials{}, errors.New("s3: no static key configured")
	}
	return aws.Credentials{
		AccessKeyID:     p.id,
		SecretAccessKey: string(p.secret.Bytes()),
		Source:          "bekup-static",
	}, nil
}

// newRetryer is the SDK's standard retryer (3 attempts) that never retries
// a netguard denial, a refused redirect or an oversized response: they are
// policy decisions, not transient failures.
func newRetryer() aws.Retryer {
	return retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 3
		o.Retryables = append([]retry.IsErrorRetryable{retry.IsErrorRetryableFunc(func(err error) aws.Ternary {
			if errors.Is(err, netguard.ErrBlockedAddress) || errors.Is(err, netguard.ErrRedirect) ||
				errors.Is(err, netguard.ErrResponseTooLarge) || errors.Is(err, netguard.ErrUnsupportedNetwork) {
				return aws.FalseTernary
			}
			return aws.UnknownTernary
		})}, o.Retryables...)
	})
}

// Destination is an S3 (or S3-compatible) destination. See the package
// documentation for the semantics.
type Destination struct {
	cfg        Config
	client     *s3.Client
	httpClient interface{ CloseIdleConnections() }
	prefix     storage.Key
	lock       bool // bucket has Object Lock enabled
	versioning bool // bucket versioning is Enabled (or Suspended with versions)
	closed     atomic.Bool
}

var _ storage.Destination = (*Destination)(nil)

// Open validates cfg, builds a client that dials only through cfg.Guard and
// detects the bucket's versioning and Object Lock state. A default lock
// mode on a bucket without Object Lock fails with storage.ErrUnsupported.
func Open(ctx context.Context, cfg Config) (*Destination, error) {
	v, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	var tlsCfg *tls.Config
	if v.roots != nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: v.roots}
	}
	hc := newHTTPClient(cfg.Guard.NewHTTPClient(tlsCfg, 0))
	if v.endpoint != nil && v.endpoint.Scheme == "http" {
		cfg.Logger.WarnContext(ctx, "s3_destination_plain_http", "reason", "dev_mode", "bucket", cfg.Bucket)
	}
	reqCk, respCk := aws.RequestChecksumCalculationWhenRequired, aws.ResponseChecksumValidationWhenRequired
	if cfg.Checksum == ChecksumWhenSupported {
		respCk = aws.ResponseChecksumValidationWhenSupported
	}
	opts := s3.Options{
		Region:                         cfg.Region,
		Credentials:                    staticProvider{id: cfg.AccessKeyID, secret: cfg.SecretAccessKey},
		HTTPClient:                     hc,
		UsePathStyle:                   cfg.PathStyle,
		RequestChecksumCalculation:     reqCk,
		ResponseChecksumValidation:     respCk,
		DisableS3ExpressSessionAuth:    aws.Bool(true),
		DisableMultiRegionAccessPoints: true,
		UseARNRegion:                   false,
		Retryer:                        newRetryer(),
	}
	if v.endpoint != nil {
		opts.BaseEndpoint = aws.String(v.endpoint.String())
	}
	d := &Destination{
		cfg:        cfg,
		client:     s3.New(opts),
		httpClient: hc,
		prefix:     v.prefix,
	}
	if err := d.detect(ctx); err != nil {
		hc.CloseIdleConnections()
		return nil, err
	}
	if cfg.LockMode != storage.LockNone && !d.lock {
		hc.CloseIdleConnections()
		return nil, fmt.Errorf("%w: the bucket does not have Object Lock enabled", storage.ErrUnsupported)
	}
	return d, nil
}

// detect reads the bucket's versioning and Object Lock configuration.
func (d *Destination) detect(ctx context.Context) error {
	ver, err := d.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &d.cfg.Bucket})
	switch {
	case err == nil:
		d.versioning = ver.Status == types.BucketVersioningStatusEnabled ||
			ver.Status == types.BucketVersioningStatusSuspended
	case isUnsupportedConfig(err):
		d.versioning = false
	default:
		return fmt.Errorf("s3: bucket versioning: %w", classify(ctx, opOther, err))
	}
	lc, err := d.client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: &d.cfg.Bucket})
	switch {
	case err == nil:
		d.lock = lc.ObjectLockConfiguration != nil &&
			lc.ObjectLockConfiguration.ObjectLockEnabled == types.ObjectLockEnabledEnabled
	case isUnsupportedConfig(err):
		d.lock = false
	default:
		return fmt.Errorf("s3: bucket object lock: %w", classify(ctx, opOther, err))
	}
	if d.lock {
		// Object Lock buckets are always versioned.
		d.versioning = true
	}
	return nil
}

// isUnsupportedConfig reports "no such configuration" answers: the bucket
// exists but has no Object Lock configuration, or the store does not
// implement the call.
func isUnsupportedConfig(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ObjectLockConfigurationNotFoundError", "NotImplemented",
			"NoSuchObjectLockConfiguration", "MethodNotAllowed":
			return true
		}
	}
	return statusOf(err) == 501
}

// Capabilities implements storage.Destination.
func (d *Destination) Capabilities() storage.Capabilities {
	c := storage.Capabilities{
		Type:                 "s3",
		SupportsObjectLock:   d.lock,
		Versioning:           d.versioning,
		NoDeleteMode:         d.cfg.NoDelete,
		ExclusiveCreate:      d.cfg.conditional(),
		ServerSideEncryption: d.cfg.SSE != SSENone,
		MaxObjectSize:        d.cfg.maxObjectSize(),
	}
	if d.cfg.Checksum == ChecksumWhenSupported {
		c.NativeChecksum = "SHA256"
	}
	return c
}

// Close implements storage.Destination. The caller still owns (and
// destroys) Config.SecretAccessKey.
func (d *Destination) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.httpClient.CloseIdleConnections()
	return nil
}

func (d *Destination) check(ctx context.Context, key storage.Key) error {
	if d.closed.Load() {
		return storage.ErrClosed
	}
	if key.IsZero() {
		return fmt.Errorf("%w: empty key", storage.ErrInvalidKey)
	}
	return ctx.Err()
}

// objectKey maps a storage key to the S3 key below the prefix.
func (d *Destination) objectKey(key storage.Key) string {
	if d.prefix.IsZero() {
		return key.String()
	}
	return d.prefix.String() + "/" + key.String()
}

// listPrefix returns the S3 prefix for listing below p ("" = everything
// below the destination prefix).
func (d *Destination) listPrefix(p storage.Key) string {
	full := p.String()
	if !d.prefix.IsZero() {
		if full == "" {
			full = d.prefix.String()
		} else {
			full = d.prefix.String() + "/" + full
		}
	}
	if full == "" {
		return ""
	}
	return full + "/"
}

// storageKey maps an S3 key back; ok is false for keys outside the prefix
// or keys that are not valid storage keys (foreign objects, folder
// markers), which are skipped.
func (d *Destination) storageKey(s string) (storage.Key, bool) {
	if !d.prefix.IsZero() {
		rest, found := strings.CutPrefix(s, d.prefix.String()+"/")
		if !found {
			return storage.Key{}, false
		}
		s = rest
	}
	k, err := storage.ParseKey(s)
	if err != nil {
		return storage.Key{}, false
	}
	return k, true
}

// Get implements storage.Destination. Objects are always streamed through
// bekup; presigned URLs are never issued.
func (d *Destination) Get(ctx context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	in := &s3.GetObjectInput{Bucket: &d.cfg.Bucket, Key: aws.String(d.objectKey(key))}
	if d.cfg.Checksum == ChecksumWhenSupported {
		in.ChecksumMode = types.ChecksumModeEnabled
	}
	out, err := d.client.GetObject(ctx, in)
	if err != nil {
		return nil, storage.ObjectInfo{}, fmt.Errorf("s3 get: %w", classify(ctx, opOther, err))
	}
	info := objectInfo(key, objectFields{
		size: out.ContentLength, mod: out.LastModified, meta: out.Metadata,
		ctype: out.ContentType, lockMode: out.ObjectLockMode, retain: out.ObjectLockRetainUntilDate,
		hold: out.ObjectLockLegalHoldStatus, version: out.VersionId, sha: out.ChecksumSHA256,
	})
	return out.Body, info, nil
}

// Stat implements storage.Destination.
func (d *Destination) Stat(ctx context.Context, key storage.Key) (storage.ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return storage.ObjectInfo{}, err
	}
	out, err := d.head(ctx, d.objectKey(key), nil)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("s3 stat: %w", classify(ctx, opOther, err))
	}
	return objectInfo(key, objectFields{
		size: out.ContentLength, mod: out.LastModified, meta: out.Metadata,
		ctype: out.ContentType, lockMode: out.ObjectLockMode, retain: out.ObjectLockRetainUntilDate,
		hold: out.ObjectLockLegalHoldStatus, version: out.VersionId, sha: out.ChecksumSHA256,
	}), nil
}

func (d *Destination) head(ctx context.Context, objKey string, version *string) (*s3.HeadObjectOutput, error) {
	in := &s3.HeadObjectInput{Bucket: &d.cfg.Bucket, Key: &objKey, VersionId: version}
	if d.cfg.Checksum == ChecksumWhenSupported {
		in.ChecksumMode = types.ChecksumModeEnabled
	}
	return d.client.HeadObject(ctx, in)
}

type objectFields struct {
	size     *int64
	mod      *time.Time
	meta     map[string]string
	ctype    *string
	lockMode types.ObjectLockMode
	retain   *time.Time
	hold     types.ObjectLockLegalHoldStatus
	version  *string
	sha      *string
}

func objectInfo(key storage.Key, f objectFields) storage.ObjectInfo {
	info := storage.ObjectInfo{
		Key:         key,
		Size:        aws.ToInt64(f.size),
		ModTime:     aws.ToTime(f.mod),
		ContentType: aws.ToString(f.ctype),
		RetainUntil: aws.ToTime(f.retain),
		LegalHold:   f.hold == types.ObjectLockLegalHoldStatusOn,
		VersionID:   aws.ToString(f.version),
	}
	if info.VersionID == "null" {
		info.VersionID = ""
	}
	switch f.lockMode {
	case types.ObjectLockModeGovernance:
		info.LockMode = storage.LockGovernance
	case types.ObjectLockModeCompliance:
		info.LockMode = storage.LockCompliance
	}
	for k, v := range f.meta {
		if strings.EqualFold(k, metaSHA256) {
			if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
				info.SHA256 = b
			}
		}
	}
	if f.sha != nil {
		info.NativeChecksum = storage.Checksum{Algorithm: "SHA256", Value: *f.sha}
	}
	return info
}

// maxListRounds bounds the internal re-listing when a whole page consists
// of skipped (foreign) keys.
const maxListRounds = 100

// List implements storage.Destination (ListObjectsV2; S3 returns keys in
// UTF-8 binary order, which for valid storage keys is byte order).
func (d *Destination) List(ctx context.Context, prefix storage.Key, opts storage.ListOptions) (storage.ListPage, error) {
	if d.closed.Load() {
		return storage.ListPage{}, storage.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return storage.ListPage{}, err
	}
	size := opts.PageSize
	if size <= 0 || size > storage.MaxListPageSize {
		size = storage.MaxListPageSize
	}
	in := &s3.ListObjectsV2Input{
		Bucket:  &d.cfg.Bucket,
		Prefix:  aws.String(d.listPrefix(prefix)),
		MaxKeys: aws.Int32(int32(size)),
	}
	if !opts.StartAfter.IsZero() {
		in.StartAfter = aws.String(d.objectKey(opts.StartAfter))
	}
	var page storage.ListPage
	for range maxListRounds {
		out, err := d.client.ListObjectsV2(ctx, in)
		if err != nil {
			return storage.ListPage{}, fmt.Errorf("s3 list: %w", classify(ctx, opOther, err))
		}
		var lastRaw string
		for _, o := range out.Contents {
			raw := aws.ToString(o.Key)
			lastRaw = raw
			k, ok := d.storageKey(raw)
			if !ok {
				continue
			}
			page.Objects = append(page.Objects, storage.ObjectInfo{
				Key: k, Size: aws.ToInt64(o.Size), ModTime: aws.ToTime(o.LastModified),
			})
		}
		page.More = aws.ToBool(out.IsTruncated)
		if !page.More && len(out.Contents) == size && lastRaw != "" {
			// Some stores (minio) report a full page as not truncated
			// even when keys follow; confirm with a one-key probe.
			more, err := d.hasMoreAfter(ctx, in.Prefix, lastRaw)
			if err != nil {
				return storage.ListPage{}, err
			}
			page.More = more
		}
		if len(page.Objects) > 0 || !page.More || lastRaw == "" {
			break
		}
		// The page held only skipped keys: continue after the last one.
		in.StartAfter = aws.String(lastRaw)
		in.ContinuationToken = nil
	}
	if n := len(page.Objects); n > 0 {
		page.Next = page.Objects[n-1].Key
	} else {
		page.More = false
	}
	return page, nil
}

// hasMoreAfter reports whether any key below prefix follows after.
func (d *Destination) hasMoreAfter(ctx context.Context, prefix *string, after string) (bool, error) {
	out, err := d.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &d.cfg.Bucket, Prefix: prefix, StartAfter: aws.String(after), MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("s3 list: %w", classify(ctx, opOther, err))
	}
	return len(out.Contents) > 0, nil
}

// Delete implements storage.Destination. On a versioned or Object Lock
// bucket it permanently removes every version of the key (never a mere
// delete marker, which would hide a locked object instead of honoring the
// lock); versions under retention or legal hold yield
// storage.ErrDeleteNotPermitted. Governance retention is never bypassed.
func (d *Destination) Delete(ctx context.Context, key storage.Key) error {
	if err := d.check(ctx, key); err != nil {
		return err
	}
	if d.cfg.NoDelete {
		return storage.ErrDeleteNotPermitted
	}
	objKey := d.objectKey(key)
	if !d.versioning {
		if _, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &d.cfg.Bucket, Key: &objKey}); err != nil {
			return fmt.Errorf("s3 delete: %w", classifyDelete(ctx, err))
		}
		return nil
	}
	versions, markers, err := d.versionsOf(ctx, objKey)
	if err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	// Data versions first: if one is locked, the delete markers stay so
	// nothing older becomes visible again.
	for _, v := range versions {
		if d.lock {
			if err := d.checkUnlocked(ctx, objKey, v); err != nil {
				return err
			}
		}
		if err := d.deleteVersion(ctx, objKey, v); err != nil {
			return err
		}
	}
	for _, v := range markers {
		if err := d.deleteVersion(ctx, objKey, v); err != nil {
			return err
		}
	}
	return nil
}

func classifyDelete(ctx context.Context, err error) error {
	if statusOf(err) == 404 {
		return nil
	}
	cerr := classify(ctx, opDelete, err)
	if errors.Is(cerr, storage.ErrNotFound) {
		return nil
	}
	return cerr
}

func (d *Destination) deleteVersion(ctx context.Context, objKey, version string) error {
	_, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &d.cfg.Bucket, Key: &objKey, VersionId: aws.String(version),
	})
	if err != nil {
		if cerr := classifyDelete(ctx, err); cerr != nil {
			return fmt.Errorf("s3 delete: %w", cerr)
		}
	}
	return nil
}

// checkUnlocked refuses a version under active retention or legal hold
// before trying to delete it (the store refuses anyway; this gives a clear
// error even when the store's refusal is a generic AccessDenied).
func (d *Destination) checkUnlocked(ctx context.Context, objKey, version string) error {
	out, err := d.head(ctx, objKey, aws.String(version))
	if err != nil {
		if cerr := classifyDelete(ctx, err); cerr != nil && !errors.Is(cerr, storage.ErrPermission) {
			return fmt.Errorf("s3 delete: %w", cerr)
		}
		return nil // missing, or no permission to read retention: let the delete decide
	}
	if out.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn ||
		(out.ObjectLockRetainUntilDate != nil && out.ObjectLockRetainUntilDate.After(time.Now())) {
		return fmt.Errorf("%w: object is under Object Lock", storage.ErrDeleteNotPermitted)
	}
	return nil
}

// versionsOf lists the version IDs of exactly objKey.
func (d *Destination) versionsOf(ctx context.Context, objKey string) (versions, markers []string, err error) {
	in := &s3.ListObjectVersionsInput{Bucket: &d.cfg.Bucket, Prefix: &objKey}
	for range maxListRounds {
		out, err := d.client.ListObjectVersions(ctx, in)
		if err != nil {
			return nil, nil, classify(ctx, opDelete, err)
		}
		for i := range out.Versions {
			v := &out.Versions[i]
			if aws.ToString(v.Key) == objKey && v.VersionId != nil {
				versions = append(versions, *v.VersionId)
			}
		}
		for _, m := range out.DeleteMarkers {
			if aws.ToString(m.Key) == objKey && m.VersionId != nil {
				markers = append(markers, *m.VersionId)
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return versions, markers, nil
		}
		in.KeyMarker, in.VersionIdMarker = out.NextKeyMarker, out.NextVersionIdMarker
		if aws.ToString(in.KeyMarker) > objKey {
			return versions, markers, nil
		}
	}
	return nil, nil, fmt.Errorf("%w: too many versions", storage.ErrUnsupported)
}

// lockNote is the Test note when Object Lock keeps the probe.
const lockNote = "object lock: the bucket's default retention protects the probe object; it is removed after its retention by the bucket lifecycle rules"

// Test implements storage.Destination. The probe is written without
// bekup's default retention (it would make every probe undeletable); if
// the bucket's own default retention still locks it, the refused delete is
// the expected outcome and is reported as passed with a note.
func (d *Destination) Test(ctx context.Context, prefix storage.Key) storage.TestResult {
	res := storage.RunProbe(ctx, probeDest{d}, prefix)
	if !d.lock || len(res.Steps) != 3 {
		return res
	}
	del := &res.Steps[2]
	if del.Name == storage.StepDelete && del.Code == storage.CodeDeleteNotPermitted {
		del.OK = true
		res.Note = lockNote
		res.OK = res.Steps[0].OK && res.Steps[1].OK
	}
	return res
}

// probeDest routes the probe's Put past the default retention.
type probeDest struct{ *Destination }

func (p probeDest) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	return p.put(ctx, key, r, opts, false)
}
