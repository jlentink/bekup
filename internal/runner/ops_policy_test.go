package runner

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// withGuard swaps the ops env's target guard.
func withGuard(t *testing.T, env *opsEnv, opts targets.Options) *targets.Guard {
	t.Helper()
	g, err := targets.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	val, err := validate.New(env.ops.val.Catalog(), g)
	if err != nil {
		t.Fatal(err)
	}
	env.ops.val = val
	return g
}

func TestGetTargetPolicy(t *testing.T) {
	env := newOpsEnv(t)
	g := withGuard(t, env, targets.Options{
		AllowCIDRs:     []string{"10.20.0.0/16", "2001:db8:20::/48"},
		AllowHosts:     []string{"DB.example.com", "bastion.example.com"},
		DenyCIDRs:      []string{"10.20.99.0/24"},
		DenyHosts:      []string{"legacy.example.com"},
		BastionTargets: []string{"bastion.example.com=10.30.0.0/24", "bastion.example.com=db.internal"},
	})
	pool := netip.MustParsePrefix("10.214.0.0/16")
	// Own networks as the executor publishes them: the pool, a per-run
	// subnet inside it, the control network and the container's address.
	if err := g.SetOwnNetworks([]netip.Prefix{
		pool, netip.MustParsePrefix("10.214.0.16/28"),
		netip.MustParsePrefix("172.30.0.0/24"), netip.MustParsePrefix("172.30.0.2/32"),
		netip.MustParsePrefix("172.30.0.0/24"),
	}); err != nil {
		t.Fatal(err)
	}
	ready := false
	env.ops.policy = targetPolicyInfo{
		runPool: pool,
		envKeys: targetEnvOverrides(map[string]config.Source{
			"targets.allow_cidrs": config.SourceEnv, "targets.deny_hosts": config.SourceFile,
			"targets.bastion_targets": config.SourceEnv, "docker.host": config.SourceEnv,
		}),
		ready: func() bool { return ready },
	}
	resp, err := env.ops.getTargetPolicy(t.Context(), &runnerpb.GetTargetPolicyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.GetAllowCidrs(), []string{"10.20.0.0/16", "2001:db8:20::/48"}) ||
		!slices.Equal(resp.GetAllowHosts(), []string{"bastion.example.com", "db.example.com"}) ||
		!slices.Equal(resp.GetDenyCidrs(), []string{"10.20.99.0/24"}) ||
		!slices.Equal(resp.GetDenyHosts(), []string{"legacy.example.com"}) {
		t.Errorf("lists = %v %v %v %v", resp.GetAllowCidrs(), resp.GetAllowHosts(), resp.GetDenyCidrs(), resp.GetDenyHosts())
	}
	if bt := resp.GetBastionTargets(); len(bt) != 1 || bt[0].GetBastion() != "bastion.example.com" ||
		!slices.Equal(bt[0].GetTargets(), []string{"10.30.0.0/24", "db.internal"}) {
		t.Errorf("bastion targets = %v", bt)
	}
	if resp.GetRunSubnetPool() != "10.214.0.0/16" ||
		!slices.Equal(resp.GetControlNetworks(), []string{"172.30.0.0/24", "172.30.0.2/32"}) || resp.GetControlNetworksKnown() {
		t.Errorf("own networks = %q %v known=%v", resp.GetRunSubnetPool(), resp.GetControlNetworks(), resp.GetControlNetworksKnown())
	}
	if want := []runnerpb.TargetsKey{runnerpb.TargetsKey_TARGETS_KEY_ALLOW_CIDRS, runnerpb.TargetsKey_TARGETS_KEY_BASTION_TARGETS}; !slices.Equal(resp.GetEnvOverrides(), want) {
		t.Errorf("env overrides = %v", resp.GetEnvOverrides())
	}
	if len(resp.GetWarnings()) != 0 || resp.GetTruncated() {
		t.Errorf("warnings %v truncated %v", resp.GetWarnings(), resp.GetTruncated())
	}
	// Every always-denied category, in the targets package's order.
	ad := resp.GetAlwaysDenied()
	if len(ad) != len(targets.AlwaysDenied()) || len(ad) != len(deniedCategories) {
		t.Fatalf("always denied = %v", ad)
	}
	if ad[0].GetCategory() != runnerpb.DeniedCategory_DENIED_CATEGORY_LOOPBACK || !slices.Equal(ad[0].GetCidrs(), []string{"127.0.0.0/8", "::1/128"}) {
		t.Errorf("first category = %v", ad[0])
	}
	for _, r := range ad {
		if r.GetCategory() == runnerpb.DeniedCategory_DENIED_CATEGORY_UNSPECIFIED {
			t.Errorf("unmapped category in %v", ad)
		}
	}
	if !strings.Contains(env.logs.String(), "rpc=GetTargetPolicy") {
		t.Error("no audit line")
	}

	// Once the executor is ready, the control networks are complete.
	ready = true
	resp, err = env.ops.getTargetPolicy(t.Context(), &runnerpb.GetTargetPolicyRequest{})
	if err != nil || !resp.GetControlNetworksKnown() {
		t.Errorf("ready: %v %v", resp.GetControlNetworksKnown(), err)
	}
}

func TestGetTargetPolicyWarningsAndEmpty(t *testing.T) {
	env := newOpsEnv(t)
	withGuard(t, env, targets.Options{})
	resp, err := env.ops.getTargetPolicy(t.Context(), &runnerpb.GetTargetPolicyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.GetWarnings(), []runnerpb.TargetPolicyWarning{runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_EMPTY_ALLOWLIST}) {
		t.Errorf("warnings = %v", resp.GetWarnings())
	}
	if len(resp.GetAllowCidrs())+len(resp.GetAllowHosts())+len(resp.GetBastionTargets()) != 0 || resp.GetRunSubnetPool() != "" ||
		resp.GetControlNetworksKnown() || len(resp.GetEnvOverrides()) != 0 {
		t.Errorf("empty guard = %v", resp)
	}
	withGuard(t, env, targets.Options{AllowCIDRs: []string{"10.0.0.0/8"}})
	resp, err = env.ops.getTargetPolicy(t.Context(), &runnerpb.GetTargetPolicyRequest{})
	if err != nil || !slices.Equal(resp.GetWarnings(), []runnerpb.TargetPolicyWarning{runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED}) {
		t.Errorf("warnings = %v %v", resp.GetWarnings(), err)
	}
}

// TestGetTargetPolicyBounded: the longest configurable lists are cut so the
// answer fits a gRPC message.
func TestGetTargetPolicyBounded(t *testing.T) {
	env := newOpsEnv(t)
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 50)
	cidrs, hosts, bastions := make([]string, 0, 1024), make([]string, 0, 1024), make([]string, 0, 1024)
	for i := range 1024 {
		cidrs = append(cidrs, fmt.Sprintf("2001:db8:%x::/48", i))
		hosts = append(hosts, fmt.Sprintf("h%04d.%s", i, long))
		bastions = append(bastions, fmt.Sprintf("b%04d.%s=t%04d.%s", i, long, i, long))
	}
	withGuard(t, env, targets.Options{AllowCIDRs: cidrs, AllowHosts: hosts, DenyCIDRs: cidrs, DenyHosts: hosts, BastionTargets: bastions})
	resp, err := env.ops.getTargetPolicy(t.Context(), &runnerpb.GetTargetPolicyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetTruncated() || len(resp.GetAllowHosts()) != maxPolicyEntries || len(resp.GetBastionTargets()) != maxPolicyEntries {
		t.Fatalf("truncated=%v hosts=%d bastions=%d", resp.GetTruncated(), len(resp.GetAllowHosts()), len(resp.GetBastionTargets()))
	}
	if n := proto.Size(resp); n > runnerpb.MaxMessageBytes/2 {
		t.Fatalf("answer %d bytes", n)
	}
}

func TestGetTargetPolicyRefusesUnknownFields(t *testing.T) {
	env := newOpsEnv(t)
	req := &runnerpb.GetTargetPolicyRequest{}
	req.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 3, protowire.VarintType), 1))
	if _, err := env.ops.getTargetPolicy(t.Context(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(env.logs.String(), "code=InvalidArgument") {
		t.Error("refusal not audited")
	}
}

func TestControlNetworks(t *testing.T) {
	pool := netip.MustParsePrefix("10.214.0.0/16")
	own := []netip.Prefix{
		netip.MustParsePrefix("172.30.0.2/32"), pool, netip.MustParsePrefix("10.214.3.0/28"),
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.30.0.0/24"), netip.MustParsePrefix("172.30.0.2/32"),
	}
	got := controlNetworks(own, pool)
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.30.0.0/24"), netip.MustParsePrefix("172.30.0.2/32")}
	if !slices.Equal(got, want) {
		t.Fatalf("controlNetworks = %v, want %v (a wider network around the pool stays)", got, want)
	}
	if got := controlNetworks(own[:1], netip.Prefix{}); len(got) != 1 {
		t.Fatalf("without a pool: %v", got)
	}
}
