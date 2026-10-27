package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/pubsub"
)

func TestSessionEventRoundTrip(t *testing.T) {
	u, s := uuid.New(), uuid.New()
	for _, ev := range []SessionEvent{
		{Kind: SessionRevoked, UserID: u, SessionID: s},
		{Kind: SessionRevoked, SessionID: s}, // login replacing an unknown user's session
		{Kind: SessionRotated, UserID: u, SessionID: s},
		{Kind: SessionsRevokedAll, UserID: u},
	} {
		p := ev.Encode()
		if err := pubsub.Validate(pubsub.SessionEvents.Name, p); err != nil {
			t.Fatalf("payload %q not valid for NOTIFY: %v", p, err)
		}
		got, err := DecodeSessionEvent(p)
		if err != nil || got != ev {
			t.Fatalf("round trip %+v: got %+v, %v", ev, got, err)
		}
	}
}

func TestDecodeSessionEventRejects(t *testing.T) {
	u, s := uuid.New().String(), uuid.New().String()
	nilID := uuid.Nil.String()
	for _, p := range []string{
		"",
		"revoked:" + u,
		"revoked:" + u + ":" + s + ":x",
		"deleted:" + u + ":" + s,
		"revoked:" + u + ":not-a-uuid",
		"revoked:" + strings.ReplaceAll(u, "-", "") + ":" + s,
		"user_revoked:" + u + ":" + s,         // all-revoked has no session
		"user_revoked:" + nilID + ":" + nilID, // ... but a user
		"revoked:" + u + ":" + nilID,          // single-session kinds need the session
	} {
		if ev, err := DecodeSessionEvent(p); err == nil {
			t.Errorf("DecodeSessionEvent(%q) = %+v, want an error", p, ev)
		}
	}
}

func FuzzDecodeSessionEvent(f *testing.F) {
	f.Add(SessionEvent{Kind: SessionRevoked, UserID: uuid.New(), SessionID: uuid.New()}.Encode())
	f.Fuzz(func(t *testing.T, p string) {
		ev, err := DecodeSessionEvent(p)
		if err != nil {
			return
		}
		if ev.Encode() != strings.ToLower(p) && ev.Encode() != p {
			t.Fatalf("decoded %q to %+v, which encodes differently", p, ev)
		}
	})
}
