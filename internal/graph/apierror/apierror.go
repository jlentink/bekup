// Package apierror defines the typed, user-facing errors of the GraphQL API
// (D-105). Only errors of type *Error reach the client with their message
// and an `extensions.code`; every other resolver error is masked by the
// error presenter as "internal error" plus the request ID, and the real
// error is logged.
//
// Messages are shown to the user verbatim: they must never contain secrets,
// connection strings, SQL, file paths or other internal details.
package apierror

import (
	"errors"
	"fmt"
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
)

// Codes lists the user-facing codes of *Error.
func Codes() []Code {
	return []Code{
		Unauthenticated, Forbidden, StepUpRequired, Requires2FAEnrollment,
		Validation, NotFound, RateLimited, Conflict, PasswordChangeRequired, SecondFactorRequired,
	}
}

// Error is a typed, user-facing GraphQL error.
type Error struct {
	Code    Code
	Message string
	// Field optionally names the input field a Validation error is about
	// (extensions.field).
	Field string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// New returns an *Error with the given code and user-facing message.
func New(code Code, message string) *Error { return &Error{Code: code, Message: message} }

// Newf is New with a formatted message. Never format secrets or internal
// errors into it.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Invalid returns a Validation error about field.
func Invalid(field, message string) *Error {
	return &Error{Code: Validation, Message: message, Field: field}
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
