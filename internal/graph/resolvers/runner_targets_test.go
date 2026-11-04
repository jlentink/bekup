package resolvers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// fakeRunnerTargets is a runner client for the target policy.
type fakeRunnerTargets struct {
	resp        *runnerpb.GetTargetPolicyResponse
	err         error
	hadDeadline bool
}

func (f *fakeRunnerTargets) GetTargetPolicy(ctx context.Context) (*runnerpb.GetTargetPolicyResponse, error) {
	_, f.hadDeadline = ctx.Deadline()
	return f.resp, f.err
}

func TestRunnerTargetPolicy(t *testing.T) {
	t.Parallel()
	f := &fakeRunnerTargets{resp: &runnerpb.GetTargetPolicyResponse{
		AllowCidrs: []string{"10.20.0.0/16"},
		AllowHosts: []string{"db.example.com"},
		DenyCidrs:  []string{"10.20.99.0/24"},
		BastionTargets: []*runnerpb.BastionTargetList{
			{Bastion: "bastion.example.com", Targets: []string{"10.30.0.0/24", "db.internal"}},
			{Bastion: "203.0.113.7"},
		},
		RunSubnetPool:        "10.214.0.0/16",
		ControlNetworks:      []string{"172.30.0.0/24"},
		ControlNetworksKnown: true,
		AlwaysDenied: []*runnerpb.AlwaysDeniedRange{
			{Category: runnerpb.DeniedCategory_DENIED_CATEGORY_LOOPBACK, Cidrs: []string{"127.0.0.0/8", "::1/128"}},
			{Category: runnerpb.DeniedCategory(99), Cidrs: []string{"1.2.3.0/24"}}, // a newer runner's category
			{Category: runnerpb.DeniedCategory_DENIED_CATEGORY_NON_GLOBAL_IPV6},
		},
		Warnings: []runnerpb.TargetPolicyWarning{
			runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED, runnerpb.TargetPolicyWarning(42),
		},
		EnvOverrides: []runnerpb.TargetsKey{runnerpb.TargetsKey_TARGETS_KEY_DENY_HOSTS},
		Truncated:    true,
	}}
	r := &Resolver{RunnerTargets: f}
	got, err := r.Query().RunnerTargetPolicy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !f.hadDeadline {
		t.Error("runner called without a deadline")
	}
	if !got.Available || got.Reason != nil || !got.Truncated || !got.ControlNetworksKnown {
		t.Errorf("flags = %+v", got)
	}
	if !slices.Equal(got.AllowCidrs, []string{"10.20.0.0/16"}) || !slices.Equal(got.AllowHosts, []string{"db.example.com"}) ||
		!slices.Equal(got.DenyCidrs, []string{"10.20.99.0/24"}) || got.DenyHosts == nil || len(got.DenyHosts) != 0 {
		t.Errorf("lists = %v %v %v %v", got.AllowCidrs, got.AllowHosts, got.DenyCidrs, got.DenyHosts)
	}
	if len(got.BastionTargets) != 2 || got.BastionTargets[0].Bastion != "bastion.example.com" ||
		!slices.Equal(got.BastionTargets[0].Targets, []string{"10.30.0.0/24", "db.internal"}) || got.BastionTargets[1].Targets == nil {
		t.Errorf("bastions = %+v", got.BastionTargets)
	}
	if got.RunSubnetPool == nil || *got.RunSubnetPool != "10.214.0.0/16" || !slices.Equal(got.ControlNetworks, []string{"172.30.0.0/24"}) {
		t.Errorf("own networks = %v %v", got.RunSubnetPool, got.ControlNetworks)
	}
	// Unknown enum values of a newer runner are dropped, never passed on.
	if len(got.AlwaysDenied) != 2 || got.AlwaysDenied[0].Category != model.RunnerDeniedCategoryLoopback ||
		got.AlwaysDenied[1].Category != model.RunnerDeniedCategoryNonGlobalIPV6 || got.AlwaysDenied[1].Cidrs == nil {
		t.Errorf("always denied = %+v", got.AlwaysDenied)
	}
	if !slices.Equal(got.Warnings, []model.RunnerTargetWarning{model.RunnerTargetWarningBastionsUnrestricted}) {
		t.Errorf("warnings = %v", got.Warnings)
	}
	if !slices.Equal(got.EnvOverrides, []model.RunnerTargetsKey{model.RunnerTargetsKeyDenyHosts}) {
		t.Errorf("env overrides = %v", got.EnvOverrides)
	}

	// An empty answer has empty lists (non-null) and no pool.
	f.resp = &runnerpb.GetTargetPolicyResponse{}
	got, err = r.Query().RunnerTargetPolicy(t.Context())
	if err != nil || !got.Available || got.AllowCidrs == nil || got.AlwaysDenied == nil || got.RunSubnetPool != nil {
		t.Errorf("empty answer = %+v %v", got, err)
	}
}

// TestRunnerTargetPolicyUnavailable: runner errors never fail the field;
// the reason is a fixed text, never the runner's message.
func TestRunnerTargetPolicyUnavailable(t *testing.T) {
	t.Parallel()
	const leak = "dial tcp 10.9.8.7:7443: secret-detail"
	for _, tt := range []struct {
		code codes.Code
		want string
	}{
		{codes.Unavailable, reasonRunnerUnreachable},
		{codes.DeadlineExceeded, reasonRunnerUnreachable},
		{codes.Unimplemented, reasonRunnerOutdated},
		{codes.PermissionDenied, reasonRunnerRefused},
		{codes.Unauthenticated, reasonRunnerRefused},
		{codes.ResourceExhausted, reasonRunnerBusy},
		{codes.Internal, reasonRunnerFailed},
		{codes.Unknown, reasonRunnerFailed},
	} {
		r := &Resolver{RunnerTargets: &fakeRunnerTargets{err: status.Error(tt.code, leak)}}
		got, err := r.Query().RunnerTargetPolicy(t.Context())
		if err != nil {
			t.Fatalf("%v: %v", tt.code, err)
		}
		if got.Available || got.Reason == nil || *got.Reason != tt.want || strings.Contains(*got.Reason, "10.9.8.7") {
			t.Errorf("%v: %+v reason %v", tt.code, got, got.Reason)
		}
		if got.AllowCidrs == nil || got.BastionTargets == nil || got.AlwaysDenied == nil || got.Warnings == nil || got.EnvOverrides == nil {
			t.Errorf("%v: null lists in %+v", tt.code, got)
		}
	}
	// A plain error (no gRPC status) is a generic failure.
	r := &Resolver{RunnerTargets: &fakeRunnerTargets{err: errors.New(leak)}}
	if got, err := r.Query().RunnerTargetPolicy(t.Context()); err != nil || *got.Reason != reasonRunnerFailed {
		t.Errorf("plain error: %+v %v", got, err)
	}
	// A request that ended returns its own error.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r = &Resolver{RunnerTargets: &fakeRunnerTargets{err: status.Error(codes.Canceled, "canceled")}}
	if _, err := r.Query().RunnerTargetPolicy(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled request: %v", err)
	}
	// Without a source the field fails (masked as an internal error).
	if _, err := (&Resolver{}).Query().RunnerTargetPolicy(t.Context()); !errors.Is(err, errRunnerTargetsNotConfigured) {
		t.Errorf("unconfigured: %v", err)
	}
}
