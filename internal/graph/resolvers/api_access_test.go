package resolvers

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/apiaccess"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func TestAPITokenErrorMapping(t *testing.T) {
	cases := []struct {
		err   error
		code  apierror.Code
		field string
	}{
		{serviceutil.FieldErr("allowedCidrs", "bad"), apierror.Validation, "allowedCidrs"},
		{fmt.Errorf("wrapped: %w", apiaccess.ErrNotFound), apierror.NotFound, ""},
		{apiaccess.ErrRevoked, apierror.Conflict, ""},
		{apiaccess.ErrForbidden, apierror.Forbidden, ""},
	}
	for _, c := range cases {
		e, ok := apierror.As(apiTokenError(c.err))
		if !ok || e.Code != c.code || e.Field != c.field {
			t.Errorf("apiTokenError(%v) = %v", c.err, e)
		}
	}
	other := errors.New("db down")
	if got := apiTokenError(other); !errors.Is(got, other) {
		t.Errorf("other errors pass through (masked by the presenter): %v", got)
	}
	if apiTokenError(nil) != nil {
		t.Error("nil")
	}
}

func TestAPITokenModel(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.7")
	now := time.Now()
	tok := apiaccess.Token{
		ID: uuid.New(), Name: "ci", Prefix: "bkp_abcdefgh",
		Jobs:       []apiaccess.TokenJob{{ID: uuid.New(), Name: "app", Deleted: true}},
		HiddenJobs: 2, CreatedByCaller: true,
		CreatedBy: &apiaccess.Creator{ID: uuid.New(), Email: "a@example.com", DisplayName: "A"},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32")},
		LastSeen:     &apiaccess.Sighting{At: now, IP: &ip, Refused: true},
	}
	m := apiTokenModel(&tok)
	if m.Prefix != "bkp_abcdefgh" || m.HiddenJobCount != 2 || !m.CreatedByMe || len(m.Jobs) != 1 || !m.Jobs[0].Deleted ||
		m.CreatedBy == nil || m.CreatedBy.Email != "a@example.com" || m.AllowAnyAddress {
		t.Fatalf("model %+v", m)
	}
	if len(m.AllowedCidrs) != 2 || m.AllowedCidrs[0] != "203.0.113.0/24" || m.AllowedCidrs[1] != "2001:db8::/32" {
		t.Fatalf("cidrs %v", m.AllowedCidrs)
	}
	if m.LastSeenAt == nil || m.LastSeenIP == nil || *m.LastSeenIP != "203.0.113.7" || !m.LastSeenRefused {
		t.Fatalf("sighting %+v", m)
	}
	// Never seen, any address, no creator shown.
	m = apiTokenModel(&apiaccess.Token{ID: uuid.New(), AllowAnyAddress: true})
	if m.LastSeenAt != nil || m.LastSeenIP != nil || m.CreatedBy != nil || len(m.AllowedCidrs) != 0 || m.AllowedCidrs == nil || m.Jobs == nil {
		t.Fatalf("empty model %+v", m)
	}
}

func TestAPITokenFieldsUnconfigured(t *testing.T) {
	r := &Resolver{}
	if _, err := r.apiTokens(t.Context(), nil); !errors.Is(err, errAPITokensUnconfigured) {
		t.Fatal(err)
	}
}
