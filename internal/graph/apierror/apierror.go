// Package apierror defines the typed, user-facing errors of the GraphQL API
// (D-105). Only errors of type *Error reach the client with their message
// and an `extensions.code`; every other resolver error is masked by the
// error presenter as "internal error" plus the request ID, and the real
// error is logged.
//
// Messages are shown to the user: they must never contain secrets,
// connection strings, SQL, file paths or other internal details. Each
// message keeps its English format and arguments (Msg, D-238); the error
// presenter renders it in the request language from the central catalog
// in internal/i18n, so every text needs a Dutch entry there.
package apierror

import (
	"errors"

	"github.com/jlentink/bekup/internal/i18n"
)

// Code is the value of `extensions.code` of a GraphQL error.
type Code string

// User-facing error codes. The web client switches on these; never rename a
// code once released.
const (
	// Unauthenticated: no (valid) session; the client goes to login.
	Unauthenticated Code = "UNAUTHENTICATED"
	// Forbidden: authenticated, but the role or ownership does not allow it.
	Forbidden Code = "FORBIDDEN"
	// StepUpRequired: a fresh second-factor check is needed (task 1.9).
	StepUpRequired Code = "STEP_UP_REQUIRED"
	// Requires2FAEnrollment: the operation needs 2FA and the user has none.
	Requires2FAEnrollment Code = "REQUIRES_2FA_ENROLLMENT"
	// Validation: the input is invalid; Field names the offending input.
	Validation Code = "VALIDATION"
	// NotFound: the object does not exist or is not visible to the caller.
	NotFound Code = "NOT_FOUND"
	// RateLimited: too many attempts; retry later.
	RateLimited Code = "RATE_LIMITED"
	// Conflict: the object changed or already exists.
	Conflict Code = "CONFLICT"
	// PasswordChangeRequired: the session signed in with a temporary
	// password and may do nothing but change it (task 1.6).
	PasswordChangeRequired Code = "PASSWORD_CHANGE_REQUIRED"
	// SecondFactorRequired: the session belongs to a login whose password
	// step succeeded and that waits for its second factor; only
	// completeSecondFactor, beginPasskeyLogin and logout work (task 1.9).
	SecondFactorRequired Code = "SECOND_FACTOR_REQUIRED"
	// ConnectionFailed: the runner could not reach or use a database
	// connection; the message is a fixed, normalized reason (task 2.11).
	ConnectionFailed Code = "CONNECTION_FAILED"
	// TLSPinMismatch: the database's certificate does not match the pinned
	// certificate (security event: audited and alerted; task 2.11).
	TLSPinMismatch Code = "TLS_PIN_MISMATCH"
	// SSHHostKeyMismatch: the SSH bastion presented another host key than
	// the pinned one (security event: audited and alerted; task 2.11).
	SSHHostKeyMismatch Code = "SSH_HOST_KEY_MISMATCH"
)

// Codes not produced by *Error but by the server itself.
const (
	// Internal marks a masked internal error (with extensions.requestId).
	Internal Code = "INTERNAL"
	// IntrospectionDisabled: introspection is not allowed for this caller.
	IntrospectionDisabled Code = "INTROSPECTION_DISABLED"
	// QueryTooDeep: the operation exceeds graphql.max_depth.
	QueryTooDeep Code = "QUERY_TOO_DEEP"
	// QueryTooComplex: the operation exceeds graphql.max_complexity (the
	// code gqlgen's complexity extension uses).
	QueryTooComplex Code = "COMPLEXITY_LIMIT_EXCEEDED"
	// SessionRotated ends an event stream whose session was rotated
	// (privilege change): the client opens the stream again with the new
	// cookie (internal/graph/streams).
	SessionRotated Code = "SESSION_ROTATED"
	// Unavailable ends an event stream when the server shuts down or the
	// session could not be checked: retry later.
	Unavailable Code = "UNAVAILABLE"
)

// Codes lists the user-facing codes of *Error.
func Codes() []Code {
	return []Code{
		Unauthenticated, Forbidden, StepUpRequired, Requires2FAEnrollment,
		Validation, NotFound, RateLimited, Conflict, PasswordChangeRequired, SecondFactorRequired,
		ConnectionFailed, TLSPinMismatch, SSHHostKeyMismatch,
	}
}

// Error is a typed, user-facing GraphQL error.
type Error struct {
	Code Code
	// Message is the English text (logs, Error()); clients get Msg in their
	// language (Localized).
	Message string
	// Msg is the translatable text (D-238).
	Msg i18n.Msg
	// Field optionally names the input field a Validation error is about
	// (extensions.field).
	Field string
	// Jobs optionally names the jobs an in-use Conflict is about
	// (extensions.jobs), so clients never parse them from the message.
	Jobs []string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Localized returns the message in l.
func (e *Error) Localized(l i18n.Locale) string {
	if e.Msg.IsZero() {
		return e.Message
	}
	return e.Msg.In(l)
}

// NewMsg returns an *Error with the given code and translatable message.
func NewMsg(code Code, m i18n.Msg) *Error {
	return &Error{Code: code, Message: m.String(), Msg: m}
}

// New returns an *Error with the given code and user-facing message (a
// constant text, shown as is; use Newf for arguments).
func New(code Code, message string) *Error { return NewMsg(code, i18n.Text(message)) }

// Newf is New with a formatted message. Never format secrets or internal
// errors into it.
func Newf(code Code, format string, args ...any) *Error {
	return NewMsg(code, i18n.M(format, args...))
}

// InUse returns a Conflict naming the jobs that still use an object.
func InUse(m i18n.Msg, jobs []string) *Error {
	e := NewMsg(Conflict, m)
	e.Jobs = jobs
	return e
}

// InvalidMsg returns a Validation error about field with a translatable
// message.
func InvalidMsg(field string, m i18n.Msg) *Error {
	return &Error{Code: Validation, Message: m.String(), Msg: m, Field: field}
}

// Invalid returns a Validation error about field (a constant text without
// shown as is; use Invalidf for arguments).
func Invalid(field, message string) *Error { return InvalidMsg(field, i18n.Text(message)) }

// Invalidf is Invalid with a formatted message.
func Invalidf(field, format string, args ...any) *Error {
	return InvalidMsg(field, i18n.M(format, args...))
}

// As returns the *Error in err's chain, if any.
func As(err error) (*Error, bool) {
	return errors.AsType[*Error](err)
}

// IsCode reports whether err carries a typed error with code c.
func IsCode(err error, c Code) bool {
	e, ok := As(err)
	return ok && e.Code == c
}
