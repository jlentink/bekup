package auth

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

type fakeChecker struct {
	kind  Factor
	users map[uuid.UUID]bool
	err   error
}

func (f fakeChecker) Kind() Factor { return f.kind }
func (f fakeChecker) HasFactor(_ context.Context, id uuid.UUID) (bool, error) {
	return f.users[id], f.err
}

func TestSecondFactors(t *testing.T) {
	ctx := context.Background()
	a, b, none := uuid.New(), uuid.New(), uuid.New()
	s := NewSecondFactors(
		fakeChecker{kind: FactorTOTP, users: map[uuid.UUID]bool{a: true}},
		fakeChecker{kind: FactorPasskey, users: map[uuid.UUID]bool{a: true, b: true}},
	)
	for _, tc := range []struct {
		id   uuid.UUID
		want []Factor
	}{
		{a, []Factor{FactorTOTP, FactorPasskey}},
		{b, []Factor{FactorPasskey}},
		{none, nil},
	} {
		got, err := s.Enrolled(ctx, tc.id)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("Enrolled = %v, %v; want %v", got, err, tc.want)
		}
		has, err := s.HasSecondFactor(ctx, tc.id)
		if err != nil || has != (len(tc.want) > 0) {
			t.Errorf("HasSecondFactor = %v, %v", has, err)
		}
	}

	// Re-registering a kind replaces it; errors deny.
	boom := errors.New("db down")
	s.Register(fakeChecker{kind: FactorPasskey, err: boom})
	if _, err := s.Enrolled(ctx, b); !errors.Is(err, boom) {
		t.Fatalf("Enrolled error = %v", err)
	}
	if has, err := s.HasSecondFactor(ctx, b); has || !errors.Is(err, boom) {
		t.Fatalf("HasSecondFactor = %v, %v", has, err)
	}
	s.Register(nil)

	var empty SecondFactors
	if has, err := empty.HasSecondFactor(ctx, a); has || err != nil {
		t.Fatalf("empty aggregator: %v %v", has, err)
	}
	if err := (AllowFactorRemoval{}).CheckFactorRemoval(ctx, a, FactorPasskey, nil); err != nil {
		t.Fatal(err)
	}
}
