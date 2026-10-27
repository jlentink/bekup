// Package model holds the GraphQL models: gqlgen-generated types
// (models_gen.go) and the custom scalar bindings.
package model

import (
	"io"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

// MarshalUUID writes u in canonical form (scalar UUID).
func MarshalUUID(u uuid.UUID) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		writeString(w, strconv.Quote(u.String()))
	})
}

// UnmarshalUUID parses a canonical UUID string (scalar UUID). Other
// encodings uuid.Parse accepts (braces, urn:uuid:, no hyphens) are refused.
func UnmarshalUUID(v any) (uuid.UUID, error) {
	s, ok := v.(string)
	if !ok || len(s) != 36 {
		return uuid.Nil, apierror.New(apierror.Validation, "invalid UUID")
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, apierror.New(apierror.Validation, "invalid UUID")
	}
	return u, nil
}

// MarshalTime writes t as RFC 3339 with nanoseconds in UTC (scalar Time).
func MarshalTime(t time.Time) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		writeString(w, strconv.Quote(t.UTC().Format(time.RFC3339Nano)))
	})
}

// UnmarshalTime parses an RFC 3339 timestamp (scalar Time).
func UnmarshalTime(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, apierror.New(apierror.Validation, "invalid time: expected an RFC 3339 string")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, apierror.New(apierror.Validation, "invalid time: expected an RFC 3339 string")
	}
	return t, nil
}

// writeString writes s; a write error means the response is gone and gqlgen
// has no way to report it from a Marshaler.
func writeString(w io.Writer, s string) {
	if _, err := io.WriteString(w, s); err != nil {
		return
	}
}
