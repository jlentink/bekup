package s3store

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // G501: Content-MD5 is an S3 transport integrity header, not a security primitive
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/jlentink/bekup/internal/storage"
)

const defaultContentType = "application/octet-stream"

// Put implements storage.Destination.
//
// Objects up to PartSize go up with one PutObject; larger ones with a
// multipart upload whose Complete makes the object visible atomically. Any
// failure (source error, checksum mismatch, cancellation, a failed part or
// a refused Complete) aborts the multipart upload on a context detached
// from the caller's cancellation, so neither an object nor an in-progress
// upload remains. "If-None-Match: *" makes the store refuse to overwrite
// (ConditionalWrites); a Stat pre-check fails early before any upload.
func (d *Destination) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	return d.put(ctx, key, r, opts, true)
}

func (d *Destination) put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions, applyDefault bool) (storage.ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.CheckPutOptions(d.Capabilities(), opts); err != nil {
		return storage.ObjectInfo{}, err
	}
	if applyDefault && opts.LockMode == storage.LockNone && d.cfg.LockMode != storage.LockNone {
		opts.LockMode = d.cfg.LockMode
		opts.RetainUntil = time.Now().AddDate(0, 0, d.cfg.RetentionDays)
	}
	if opts.ContentType == "" {
		opts.ContentType = defaultContentType
	}
	objKey := d.objectKey(key)

	// Fail early (and, without conditional writes, as the only guard).
	if _, err := d.head(ctx, objKey, nil); err == nil {
		return storage.ObjectInfo{}, storage.ErrAlreadyExists
	} else if cerr := classify(ctx, opOther, err); !errors.Is(cerr, storage.ErrNotFound) {
		return storage.ObjectInfo{}, fmt.Errorf("s3 put: %w", cerr)
	}

	src := &srcReader{ctx: ctx, r: r, h: sha256.New(), limit: d.cfg.maxObjectSize()}
	first := make([]byte, d.cfg.PartSize)
	n, eof, err := src.fill(first)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	u := &upload{d: d, objKey: objKey, opts: opts}
	var res result
	if eof {
		res, err = u.single(ctx, first[:n], src)
	} else {
		res, err = u.multipart(ctx, first, src)
	}
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info := storage.ObjectInfo{
		Key:            key,
		Size:           src.n,
		ModTime:        time.Now().UTC(),
		SHA256:         res.sum,
		ContentType:    opts.ContentType,
		RetainUntil:    opts.RetainUntil,
		LockMode:       opts.LockMode,
		LegalHold:      opts.LegalHold,
		VersionID:      res.version,
		NativeChecksum: res.checksum,
	}
	if info.VersionID == "null" {
		info.VersionID = ""
	}
	return info, nil
}

// srcReader hashes and counts the source, checks ctx before every read and
// enforces the maximum object size.
type srcReader struct {
	ctx   context.Context
	r     io.Reader
	h     hash.Hash
	n     int64
	limit int64
}

func (s *srcReader) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	m, err := s.r.Read(p)
	if m > 0 {
		s.n += int64(m)
		if s.n > s.limit {
			return 0, fmt.Errorf("%w: object exceeds the maximum object size for this part size", storage.ErrUnsupported)
		}
		s.h.Write(p[:m])
	}
	switch {
	case err == nil, errors.Is(err, io.EOF):
		return m, err
	default:
		return m, fmt.Errorf("%w: %w", storage.ErrSource, err)
	}
}

// fill reads until buf is full or the source ends.
func (s *srcReader) fill(buf []byte) (n int, eof bool, err error) {
	n, err = io.ReadFull(s, buf)
	switch {
	case err == nil:
		return n, false, nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		if cerr := s.ctx.Err(); cerr != nil {
			return n, true, cerr
		}
		return n, true, nil
	default:
		return n, false, err
	}
}

// upload is one Put in progress.
type upload struct {
	d      *Destination
	objKey string
	opts   storage.PutOptions
}

type result struct {
	sum      []byte
	version  string
	checksum storage.Checksum
}

// objHeaders are the object-level request fields shared by PutObject and
// CreateMultipartUpload.
type objHeaders struct {
	ctype    *string
	meta     map[string]string
	sse      types.ServerSideEncryption
	kms      *string
	class    types.StorageClass
	lockMode types.ObjectLockMode
	retain   *time.Time
	hold     types.ObjectLockLegalHoldStatus
	ifNone   *string
}

func (u *upload) headers(sum []byte) objHeaders {
	c := &u.d.cfg
	h := objHeaders{ctype: aws.String(u.opts.ContentType)}
	if len(sum) == sha256.Size {
		h.meta = map[string]string{metaSHA256: hex.EncodeToString(sum)}
	}
	switch c.SSE {
	case SSENone:
	case SSES3:
		h.sse = types.ServerSideEncryptionAes256
	case SSEKMS:
		h.sse = types.ServerSideEncryptionAwsKms
		if c.KMSKeyID != "" {
			h.kms = aws.String(c.KMSKeyID)
		}
	}
	if c.StorageClass != "" {
		h.class = types.StorageClass(c.StorageClass)
	}
	switch u.opts.LockMode {
	case storage.LockNone:
	case storage.LockGovernance:
		h.lockMode = types.ObjectLockModeGovernance
	case storage.LockCompliance:
		h.lockMode = types.ObjectLockModeCompliance
	}
	if !u.opts.RetainUntil.IsZero() {
		t := u.opts.RetainUntil.UTC()
		h.retain = &t
	}
	if u.opts.LegalHold {
		h.hold = types.ObjectLockLegalHoldStatusOn
	}
	if c.conditional() {
		h.ifNone = aws.String("*")
	}
	return h
}

// bodyChecks returns the per-request integrity headers for body: a SHA-256
// checksum in when_supported mode, Content-MD5 on Object Lock buckets in
// when_required mode (the protocol requires one of them there).
func (u *upload) bodyChecks(body []byte) (sha, md *string) {
	switch {
	case u.d.cfg.Checksum == ChecksumWhenSupported:
		s := sha256.Sum256(body)
		return aws.String(base64.StdEncoding.EncodeToString(s[:])), nil
	case u.d.lock:
		m := md5.Sum(body) //nolint:gosec // G401: Content-MD5 transport integrity header required by S3
		return nil, aws.String(base64.StdEncoding.EncodeToString(m[:]))
	}
	return nil, nil
}

func (u *upload) single(ctx context.Context, body []byte, src *srcReader) (result, error) {
	sum := src.h.Sum(nil)
	if err := storage.VerifySHA256(u.opts.ExpectedSHA256, sum); err != nil {
		return result{}, err
	}
	h := u.headers(sum)
	sha, md := u.bodyChecks(body)
	out, err := u.d.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    &u.d.cfg.Bucket,
		Key:                       &u.objKey,
		Body:                      bytes.NewReader(body),
		ContentLength:             aws.Int64(int64(len(body))),
		ContentType:               h.ctype,
		Metadata:                  h.meta,
		ServerSideEncryption:      h.sse,
		SSEKMSKeyId:               h.kms,
		StorageClass:              h.class,
		ObjectLockMode:            h.lockMode,
		ObjectLockRetainUntilDate: h.retain,
		ObjectLockLegalHoldStatus: h.hold,
		IfNoneMatch:               h.ifNone,
		ChecksumSHA256:            sha,
		ContentMD5:                md,
	})
	if err != nil {
		return result{}, fmt.Errorf("s3 put: %w", classify(ctx, opPut, err))
	}
	res := result{sum: sum, version: aws.ToString(out.VersionId)}
	if out.ChecksumSHA256 != nil {
		res.checksum = storage.Checksum{Algorithm: "SHA256", Value: *out.ChecksumSHA256}
	}
	return res, nil
}

func (u *upload) multipart(ctx context.Context, first []byte, src *srcReader) (result, error) {
	c := &u.d.cfg
	// The full SHA-256 is only known at the end; it is stored as metadata
	// when the caller supplied it up front (S3 metadata cannot be changed
	// after Complete without rewriting the object).
	h := u.headers(u.opts.ExpectedSHA256)
	in := &s3.CreateMultipartUploadInput{
		Bucket:                    &c.Bucket,
		Key:                       &u.objKey,
		ContentType:               h.ctype,
		Metadata:                  h.meta,
		ServerSideEncryption:      h.sse,
		SSEKMSKeyId:               h.kms,
		StorageClass:              h.class,
		ObjectLockMode:            h.lockMode,
		ObjectLockRetainUntilDate: h.retain,
		ObjectLockLegalHoldStatus: h.hold,
	}
	if c.Checksum == ChecksumWhenSupported {
		in.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
	}
	created, err := u.d.client.CreateMultipartUpload(ctx, in)
	if err != nil {
		return result{}, fmt.Errorf("s3 put: %w", classify(ctx, opPut, err))
	}
	uploadID := created.UploadId

	parts, err := u.uploadParts(ctx, first, src, uploadID)
	if err == nil {
		sum := src.h.Sum(nil)
		if err = storage.VerifySHA256(u.opts.ExpectedSHA256, sum); err == nil {
			var res result
			res, err = u.complete(ctx, uploadID, parts, h.ifNone, src.n, sum)
			if err == nil {
				res.sum = sum
				return res, nil
			}
		}
	}
	u.abort(ctx, uploadID)
	return result{}, err
}

// uploadParts streams the remaining source into parts, at most
// Concurrency in flight (and in memory) at a time.
func (u *upload) uploadParts(ctx context.Context, first []byte, src *srcReader, uploadID *string) ([]types.CompletedPart, error) {
	c := &u.d.cfg
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		partErr error
		parts   []types.CompletedPart
	)
	free := make(chan []byte, c.Concurrency)
	allocated := 1
	getBuf := func() ([]byte, error) {
		select {
		case b := <-free:
			return b, nil
		default:
		}
		if allocated < c.Concurrency {
			allocated++
			return make([]byte, c.PartSize), nil
		}
		select {
		case b := <-free:
			return b, nil
		case <-gctx.Done():
			return nil, gctx.Err()
		}
	}
	send := func(num int32, buf []byte, n int) {
		body := buf[:n]
		sha, md := u.bodyChecks(body)
		wg.Go(func() {
			defer func() { free <- buf }()
			out, err := u.d.client.UploadPart(gctx, &s3.UploadPartInput{
				Bucket:         &c.Bucket,
				Key:            &u.objKey,
				UploadId:       uploadID,
				PartNumber:     aws.Int32(num),
				Body:           bytes.NewReader(body),
				ContentLength:  aws.Int64(int64(n)),
				ChecksumSHA256: sha,
				ContentMD5:     md,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if partErr == nil {
					partErr = err
				}
				cancel()
				return
			}
			parts = append(parts, types.CompletedPart{
				PartNumber: aws.Int32(num), ETag: out.ETag, ChecksumSHA256: out.ChecksumSHA256,
			})
		})
	}

	var readErr error
	send(1, first, len(first))
	for num := int32(2); ; num++ {
		buf, err := getBuf()
		if err != nil {
			break // a part failed (partErr) or the caller canceled
		}
		n, eof, err := src.fill(buf)
		if err != nil {
			free <- buf
			readErr = err
			break
		}
		if n == 0 {
			free <- buf
			break
		}
		if num > MaxParts {
			free <- buf
			readErr = fmt.Errorf("%w: more than %d parts", storage.ErrUnsupported, MaxParts)
			break
		}
		send(num, buf, n)
		if eof {
			break
		}
	}
	wg.Wait()

	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case readErr != nil:
		return nil, readErr
	case partErr != nil:
		return nil, fmt.Errorf("s3 put: %w", classify(ctx, opPut, partErr))
	}
	slices.SortFunc(parts, func(a, b types.CompletedPart) int {
		return int(aws.ToInt32(a.PartNumber) - aws.ToInt32(b.PartNumber))
	})
	return parts, nil
}

// complete finishes the multipart upload. Once every part is stored the
// outcome must not depend on the caller's cancellation (a Complete the
// server committed while the caller gave up would otherwise be reported
// as a failure and leave an orphaned object), so it runs on a context
// detached from ctx with its own bound. When Complete fails or its answer
// is lost (an SDK retry of a committed Complete gets NoSuchUpload or
// PreconditionFailed), the object is looked up: it counts as uploaded
// only if it exists with the expected size and at least one strong
// content check (SHA-256 metadata, composite native checksum or composite
// ETag) matches and none mismatches. Otherwise the Complete error stands.
func (u *upload) complete(ctx context.Context, uploadID *string, parts []types.CompletedPart, ifNone *string, size int64, sum []byte) (result, error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), completeTimeout)
	defer cancel()
	out, err := u.d.client.CompleteMultipartUpload(cctx, &s3.CompleteMultipartUploadInput{
		Bucket:          &u.d.cfg.Bucket,
		Key:             &u.objKey,
		UploadId:        uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
		IfNoneMatch:     ifNone,
	})
	if err == nil {
		res := result{version: aws.ToString(out.VersionId)}
		if out.ChecksumSHA256 != nil {
			res.checksum = storage.Checksum{Algorithm: "SHA256", Value: *out.ChecksumSHA256}
		}
		return res, nil
	}
	cerr := fmt.Errorf("s3 put: %w", classify(cctx, opPut, err))
	if res, ok := u.verifyCommitted(ctx, parts, size, sum); ok {
		u.d.cfg.Logger.WarnContext(ctx, "s3_complete_verified_after_error",
			"bucket", u.d.cfg.Bucket, "code", string(storage.Classify(cerr)))
		return res, nil
	}
	return result{}, cerr
}

// verifyCommitted looks the object up after a failed Complete (on a
// detached, bounded context) and reports whether it is exactly the
// uploaded content.
func (u *upload) verifyCommitted(parent context.Context, parts []types.CompletedPart, size int64, sum []byte) (result, bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), abortTimeout)
	defer cancel()
	out, err := u.d.head(ctx, u.objKey, nil)
	if err != nil {
		return result{}, false
	}
	if aws.ToInt64(out.ContentLength) != size {
		return result{}, false
	}
	strong := 0
	for k, v := range out.Metadata {
		if !strings.EqualFold(k, metaSHA256) {
			continue
		}
		b, err := hex.DecodeString(v)
		if err != nil || !bytes.Equal(b, sum) {
			return result{}, false
		}
		strong++
	}
	if got := aws.ToString(out.ChecksumSHA256); got != "" {
		if want, ok := compositeChecksum(parts); ok {
			if !sameComposite(got, want, len(parts)) {
				return result{}, false
			}
			strong++
		}
	}
	if got := strings.Trim(aws.ToString(out.ETag), `"`); got != "" {
		if want, ok := compositeETag(parts); ok {
			if !strings.EqualFold(got, want) {
				return result{}, false
			}
			strong++
		}
	}
	if strong == 0 {
		return result{}, false
	}
	res := result{version: aws.ToString(out.VersionId)}
	if out.ChecksumSHA256 != nil {
		res.checksum = storage.Checksum{Algorithm: "SHA256", Value: *out.ChecksumSHA256}
	}
	return res, true
}

// compositeChecksum is the S3 composite SHA-256 of a multipart object:
// base64(sha256(concat(raw part checksums))), without the "-N" suffix.
func compositeChecksum(parts []types.CompletedPart) (string, bool) {
	h := sha256.New()
	for _, p := range parts {
		raw, err := base64.StdEncoding.DecodeString(aws.ToString(p.ChecksumSHA256))
		if err != nil || len(raw) != sha256.Size {
			return "", false
		}
		h.Write(raw)
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), len(parts) > 0
}

// sameComposite compares a returned composite checksum ("x" or "x-N")
// with the expected one.
func sameComposite(got, want string, n int) bool {
	if base, suffix, ok := strings.Cut(got, "-"); ok {
		return base == want && suffix == strconv.Itoa(n)
	}
	return got == want
}

// compositeETag is the multipart ETag "hex(md5(concat(part MD5s)))-N"; it
// only exists when every part ETag is a plain MD5 (not with SSE-KMS/SSE-C).
func compositeETag(parts []types.CompletedPart) (string, bool) {
	h := md5.New() //nolint:gosec // G401: recomputes the S3 multipart ETag, an integrity cross-check alongside SHA-256
	for _, p := range parts {
		raw, err := hex.DecodeString(strings.Trim(aws.ToString(p.ETag), `"`))
		if err != nil || len(raw) != md5.Size {
			return "", false
		}
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(parts)), len(parts) > 0
}

// abort cancels a multipart upload on a context detached from the
// caller's cancellation (a canceled Put must not leave the upload behind).
func (u *upload) abort(parent context.Context, uploadID *string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), abortTimeout)
	defer cancel()
	_, err := u.d.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: &u.d.cfg.Bucket, Key: &u.objKey, UploadId: uploadID,
	})
	if err != nil && !errors.Is(classify(ctx, opOther, err), storage.ErrNotFound) {
		u.d.cfg.Logger.WarnContext(ctx, "s3_abort_multipart_failed",
			"bucket", u.d.cfg.Bucket, "code", string(storage.Classify(classify(ctx, opOther, err))))
	}
}

// CleanupIncompleteUploads aborts multipart uploads below the destination
// prefix that were started before cutoff (crash leftovers). A bucket
// lifecycle rule "AbortIncompleteMultipartUpload" is the recommended
// backstop; this is for stores without lifecycle support. It works in
// no-delete mode too (an incomplete upload is not an object).
func (d *Destination) CleanupIncompleteUploads(ctx context.Context, cutoff time.Time) (int, error) {
	if d.closed.Load() {
		return 0, storage.ErrClosed
	}
	in := &s3.ListMultipartUploadsInput{Bucket: &d.cfg.Bucket, Prefix: aws.String(d.listPrefix(storage.Key{}))}
	aborted := 0
	for range maxListRounds {
		out, err := d.client.ListMultipartUploads(ctx, in)
		if err != nil {
			return aborted, fmt.Errorf("s3 list uploads: %w", classify(ctx, opOther, err))
		}
		for _, up := range out.Uploads {
			if up.Initiated == nil || !up.Initiated.Before(cutoff) {
				continue
			}
			if _, err := d.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket: &d.cfg.Bucket, Key: up.Key, UploadId: up.UploadId,
			}); err != nil {
				if cerr := classify(ctx, opOther, err); !errors.Is(cerr, storage.ErrNotFound) {
					return aborted, fmt.Errorf("s3 abort upload: %w", cerr)
				}
				continue
			}
			aborted++
		}
		if !aws.ToBool(out.IsTruncated) {
			return aborted, nil
		}
		in.KeyMarker, in.UploadIdMarker = out.NextKeyMarker, out.NextUploadIdMarker
	}
	return aborted, nil
}
