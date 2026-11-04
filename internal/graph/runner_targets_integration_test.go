//go:build integration

package graph_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// fakeTargetPolicy answers like the runner client.
type fakeTargetPolicy struct {
	resp *runnerpb.GetTargetPolicyResponse
	err  error
}

func (f *fakeTargetPolicy) GetTargetPolicy(context.Context) (*runnerpb.GetTargetPolicyResponse, error) {
	return f.resp, f.err
}

const runnerTargetPolicyQuery = `{ runnerTargetPolicy { available reason allowCidrs allowHosts
	bastionTargets { bastion targets } runSubnetPool alwaysDenied { category cidrs } warnings envOverrides } }`

// TestRunnerTargetPolicyGraphQL: admins read the runner's target
// allowlist; nobody else does (D-223); an unreachable runner answers
// available: false instead of an error.
func TestRunnerTargetPolicyGraphQL(t *testing.T) {
	src := &fakeTargetPolicy{resp: &runnerpb.GetTargetPolicyResponse{
		AllowCidrs:     []string{"10.20.0.0/16"},
		BastionTargets: []*runnerpb.BastionTargetList{{Bastion: "bastion.example.com", Targets: []string{"db.internal"}}},
		RunSubnetPool:  "10.214.0.0/16",
		AlwaysDenied:   []*runnerpb.AlwaysDeniedRange{{Category: runnerpb.DeniedCategory_DENIED_CATEGORY_LINK_LOCAL, Cidrs: []string{"169.254.0.0/16"}}},
		Warnings:       []runnerpb.TargetPolicyWarning{runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED},
		EnvOverrides:   []runnerpb.TargetsKey{runnerpb.TargetsKey_TARGETS_KEY_ALLOW_CIDRS},
	}}
	e := newRunEnv(t, func(_ *authEnv, o *graph.Options) { o.RunnerTargets = src })
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC := e.mustLogin("admin@example.com")

	type policy struct {
		Available      bool
		Reason         *string
		AllowCidrs     []string
		AllowHosts     []string
		BastionTargets []struct {
			Bastion string
			Targets []string
		}
		RunSubnetPool *string
		AlwaysDenied  []struct {
			Category string
			Cidrs    []string
		}
		Warnings     []string
		EnvOverrides []string
	}
	var p policy
	e.mustGQL(adminC, runnerTargetPolicyQuery, nil).field(t, "runnerTargetPolicy", &p)
	if !p.Available || p.Reason != nil || len(p.AllowCidrs) != 1 || p.AllowHosts == nil || len(p.BastionTargets) != 1 ||
		p.RunSubnetPool == nil || *p.RunSubnetPool != "10.214.0.0/16" || len(p.AlwaysDenied) != 1 || p.AlwaysDenied[0].Category != "LINK_LOCAL" ||
		len(p.Warnings) != 1 || p.Warnings[0] != "BASTIONS_UNRESTRICTED" || len(p.EnvOverrides) != 1 || p.EnvOverrides[0] != "ALLOW_CIDRS" {
		t.Errorf("runnerTargetPolicy = %+v", p)
	}

	// Admins only.
	for _, email := range []string{"maint@example.com", "owner@example.com"} {
		if r := e.gql(e.mustLogin(email), runnerTargetPolicyQuery, nil); r.code() != "FORBIDDEN" {
			t.Errorf("%s: code %q", email, r.code())
		}
	}
	if r := e.gql("", runnerTargetPolicyQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: code %q", r.code())
	}

	// An unreachable runner: no error, a fixed reason, empty lists.
	src.resp, src.err = nil, status.Error(codes.Unavailable, "connection refused 10.1.2.3")
	p = policy{}
	e.mustGQL(adminC, runnerTargetPolicyQuery, nil).field(t, "runnerTargetPolicy", &p)
	if p.Available || p.Reason == nil || *p.Reason == "" || p.AllowCidrs == nil || len(p.AllowCidrs) != 0 || p.RunSubnetPool != nil {
		t.Errorf("unavailable = %+v", p)
	}
}
