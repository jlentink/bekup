package s3store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/smithy-go"

	"github.com/jlentink/bekup/internal/storage"
)

// op names the S3 operation family for error mapping.
type op int

const (
	opOther op = iota
	opPut
	opDelete
)

// statusCoder is implemented by the SDK's HTTP response errors.
type statusCoder interface{ HTTPStatusCode() int }

// classify maps an SDK error to the storage sentinels and codes. The raw
// error stays in the chain (for admins and server logs); users only ever see
// the storage.ErrorCode message. A missing bucket and a refused request map
// to the same permission class, so the code never reveals whether a bucket
// exists. Every transport failure (refused, unresolvable, TLS, netguard
// denial, timeout) maps to one network class, like netguard.Normalize, so a
// destination test cannot be used as a port scanner.
func classify(ctx context.Context, o op, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%w: %w", cerr, err)
		}
	}
	if errors.Is(err, storage.ErrSource) {
		return err
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return classifyAPI(o, apiErr.ErrorCode(), apiErr.ErrorMessage(), statusOf(err), err)
	}
	if st := statusOf(err); st != 0 {
		return classifyAPI(o, "", "", st, err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", context.Canceled, err)
	}
	return storage.WithCode(storage.CodeNetwork, fmt.Errorf("s3: request failed: %w", err))
}

func statusOf(err error) int {
	var sc statusCoder
	if errors.As(err, &sc) {
		return sc.HTTPStatusCode()
	}
	return 0
}

// lockMessage reports whether a refusal names Object Lock / WORM protection.
func lockMessage(code, msg string) bool {
	if code == "ObjectLocked" || code == "InvalidRetentionPeriod" {
		return true
	}
	m := strings.ToLower(msg)
	return strings.Contains(m, "object lock") || strings.Contains(m, "worm") ||
		strings.Contains(m, "object is locked") || strings.Contains(m, "retention")
}

func classifyAPI(o op, code, msg string, status int, err error) error {
	wrap := func(sentinel error) error { return fmt.Errorf("%w: %w", sentinel, err) }
	switch code {
	case "NoSuchKey", "NotFound", "NoSuchVersion":
		return wrap(storage.ErrNotFound)
	case "NoSuchBucket", "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch",
		"AllAccessDisabled", "AccountProblem", "InvalidToken", "ExpiredToken",
		"InvalidObjectState", "AuthorizationHeaderMalformed", "InvalidBucketName":
		if o == opDelete && lockMessage(code, msg) {
			return wrap(storage.ErrDeleteNotPermitted)
		}
		return wrap(storage.ErrPermission)
	case "ObjectLocked":
		if o == opDelete {
			return wrap(storage.ErrDeleteNotPermitted)
		}
		return wrap(storage.ErrPermission)
	case "PreconditionFailed", "ConditionalRequestConflict":
		if o == opPut {
			return wrap(storage.ErrAlreadyExists)
		}
	case "BadDigest", "InvalidDigest", "XAmzContentSHA256Mismatch", "XAmzContentChecksumMismatch":
		return wrap(storage.ErrChecksumMismatch)
	case "EntityTooLarge", "NotImplemented", "InvalidStorageClass", "MissingContentMD5",
		"ObjectLockConfigurationNotFoundError", "InvalidBucketState":
		return wrap(storage.ErrUnsupported)
	case "RequestTimeout", "RequestTimeTooSkewed":
		return storage.WithCode(storage.CodeTimeout, err)
	case "SlowDown", "ServiceUnavailable", "InternalError":
		return storage.WithCode(storage.CodeNetwork, err)
	case "InvalidArgument", "InvalidRequest", "KeyTooLongError", "MalformedXML":
		if o == opDelete && lockMessage(code, msg) {
			return wrap(storage.ErrDeleteNotPermitted)
		}
		return wrap(storage.ErrInvalidArgument)
	}
	switch status {
	case http.StatusNotFound:
		return wrap(storage.ErrNotFound)
	case http.StatusUnauthorized, http.StatusForbidden:
		if o == opDelete && lockMessage(code, msg) {
			return wrap(storage.ErrDeleteNotPermitted)
		}
		return wrap(storage.ErrPermission)
	case http.StatusPreconditionFailed, http.StatusConflict:
		if o == opPut {
			return wrap(storage.ErrAlreadyExists)
		}
	case http.StatusNotImplemented:
		return wrap(storage.ErrUnsupported)
	case http.StatusRequestTimeout:
		return storage.WithCode(storage.CodeTimeout, err)
	case http.StatusMovedPermanently, http.StatusTemporaryRedirect:
		// Wrong region or endpoint; redirects are never followed.
		return wrap(storage.ErrPermission)
	}
	if status >= 500 {
		return storage.WithCode(storage.CodeNetwork, err)
	}
	return storage.WithCode(storage.CodeInternal, err)
}
