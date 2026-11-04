package resolvers

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// The runner's target allowlist (D-223): a read-only view of runner.toml
// [targets] for admins. The directive and the root-field gate check the
// admin role.

// RunnerTargetPolicySource asks the runner for its target allowlist
// (*runnerclient.Client).
type RunnerTargetPolicySource interface {
	GetTargetPolicy(ctx context.Context) (*runnerpb.GetTargetPolicyResponse, error)
}

// runnerPolicyTimeout bounds the runner call: an unreachable runner must
// not hold the settings page.
const runnerPolicyTimeout = 5 * time.Second

var errRunnerTargetsNotConfigured = errors.New("resolvers: runner target policy source not configured")

// Curated reasons for an unavailable allowlist; the runner's error text is
// never shown.
const (
	reasonRunnerUnreachable = "The runner could not be reached. Check that it is running; the list appears here once it answers."
	reasonRunnerOutdated    = "The runner is older than this bekup version and cannot report its target allowlist. Update the runner."
	reasonRunnerRefused     = "The runner refused the request. Check that serve's client certificate is listed in the runner's tls.allowed_client_sans."
	reasonRunnerBusy        = "The runner is busy. Try again in a moment."
	reasonRunnerFailed      = "The runner could not report its target allowlist. The runner's log has the details."
)

// runnerPolicyReason maps a runner call error to its curated reason.
func runnerPolicyReason(err error) string {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return reasonRunnerUnreachable
	case codes.Unimplemented:
		return reasonRunnerOutdated
	case codes.PermissionDenied, codes.Unauthenticated:
		return reasonRunnerRefused
	case codes.ResourceExhausted:
		return reasonRunnerBusy
	default:
		return reasonRunnerFailed
	}
}

func (r *Resolver) runnerTargetPolicy(ctx context.Context) (*model.RunnerTargetPolicy, error) {
	if r.RunnerTargets == nil {
		return nil, errRunnerTargetsNotConfigured
	}
	cctx, cancel := context.WithTimeout(ctx, runnerPolicyTimeout)
	defer cancel()
	resp, err := r.RunnerTargets.GetTargetPolicy(cctx)
	if err != nil {
		if ctx.Err() != nil {
			// The request itself ended; nobody reads the answer.
			return nil, ctx.Err()
		}
		reason := runnerPolicyReason(err)
		return emptyRunnerTargetPolicy(&reason), nil
	}
	return runnerTargetPolicyModel(resp), nil
}

func emptyRunnerTargetPolicy(reason *string) *model.RunnerTargetPolicy {
	return &model.RunnerTargetPolicy{
		Reason:     reason,
		AllowCidrs: []string{}, AllowHosts: []string{}, DenyCidrs: []string{}, DenyHosts: []string{},
		BastionTargets: []model.RunnerBastionTargets{}, ControlNetworks: []string{},
		AlwaysDenied: []model.RunnerDeniedRange{}, Warnings: []model.RunnerTargetWarning{},
		EnvOverrides: []model.RunnerTargetsKey{},
	}
}

var deniedCategoryModels = map[runnerpb.DeniedCategory]model.RunnerDeniedCategory{
	runnerpb.DeniedCategory_DENIED_CATEGORY_LOOPBACK:             model.RunnerDeniedCategoryLoopback,
	runnerpb.DeniedCategory_DENIED_CATEGORY_UNSPECIFIED_ADDRESS:  model.RunnerDeniedCategoryUnspecified,
	runnerpb.DeniedCategory_DENIED_CATEGORY_LINK_LOCAL:           model.RunnerDeniedCategoryLinkLocal,
	runnerpb.DeniedCategory_DENIED_CATEGORY_CLOUD_METADATA:       model.RunnerDeniedCategoryCloudMetadata,
	runnerpb.DeniedCategory_DENIED_CATEGORY_MULTICAST_BROADCAST:  model.RunnerDeniedCategoryMulticastBroadcast,
	runnerpb.DeniedCategory_DENIED_CATEGORY_RESERVED:             model.RunnerDeniedCategoryReserved,
	runnerpb.DeniedCategory_DENIED_CATEGORY_IPV4_IN_IPV6:         model.RunnerDeniedCategoryIPV4InIPV6,
	runnerpb.DeniedCategory_DENIED_CATEGORY_TEREDO:               model.RunnerDeniedCategoryTeredo,
	runnerpb.DeniedCategory_DENIED_CATEGORY_EMBEDDED_DENIED_IPV4: model.RunnerDeniedCategoryEmbeddedDeniedIPV4,
	runnerpb.DeniedCategory_DENIED_CATEGORY_NON_GLOBAL_IPV6:      model.RunnerDeniedCategoryNonGlobalIPV6,
}

var targetWarningModels = map[runnerpb.TargetPolicyWarning]model.RunnerTargetWarning{
	runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_EMPTY_ALLOWLIST:       model.RunnerTargetWarningEmptyAllowlist,
	runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED: model.RunnerTargetWarningBastionsUnrestricted,
}

var targetsKeyModels = map[runnerpb.TargetsKey]model.RunnerTargetsKey{
	runnerpb.TargetsKey_TARGETS_KEY_ALLOW_CIDRS:     model.RunnerTargetsKeyAllowCidrs,
	runnerpb.TargetsKey_TARGETS_KEY_ALLOW_HOSTS:     model.RunnerTargetsKeyAllowHosts,
	runnerpb.TargetsKey_TARGETS_KEY_DENY_CIDRS:      model.RunnerTargetsKeyDenyCidrs,
	runnerpb.TargetsKey_TARGETS_KEY_DENY_HOSTS:      model.RunnerTargetsKeyDenyHosts,
	runnerpb.TargetsKey_TARGETS_KEY_BASTION_TARGETS: model.RunnerTargetsKeyBastionTargets,
}

// enumModels maps the known values of in; unknown ones (a newer runner)
// are dropped.
func enumModels[K comparable, V any](in []K, known map[K]V) []V {
	out := make([]V, 0, len(in))
	for _, v := range in {
		if m, ok := known[v]; ok {
			out = append(out, m)
		}
	}
	return out
}

func runnerTargetPolicyModel(resp *runnerpb.GetTargetPolicyResponse) *model.RunnerTargetPolicy {
	m := emptyRunnerTargetPolicy(nil)
	m.Available = true
	m.AllowCidrs = nonNil(resp.GetAllowCidrs())
	m.AllowHosts = nonNil(resp.GetAllowHosts())
	m.DenyCidrs = nonNil(resp.GetDenyCidrs())
	m.DenyHosts = nonNil(resp.GetDenyHosts())
	for _, b := range resp.GetBastionTargets() {
		m.BastionTargets = append(m.BastionTargets, model.RunnerBastionTargets{Bastion: b.GetBastion(), Targets: nonNil(b.GetTargets())})
	}
	if p := resp.GetRunSubnetPool(); p != "" {
		m.RunSubnetPool = &p
	}
	m.ControlNetworks = nonNil(resp.GetControlNetworks())
	m.ControlNetworksKnown = resp.GetControlNetworksKnown()
	for _, d := range resp.GetAlwaysDenied() {
		if c, ok := deniedCategoryModels[d.GetCategory()]; ok {
			m.AlwaysDenied = append(m.AlwaysDenied, model.RunnerDeniedRange{Category: c, Cidrs: nonNil(d.GetCidrs())})
		}
	}
	m.Warnings = enumModels(resp.GetWarnings(), targetWarningModels)
	m.EnvOverrides = enumModels(resp.GetEnvOverrides(), targetsKeyModels)
	m.Truncated = resp.GetTruncated()
	return m
}
