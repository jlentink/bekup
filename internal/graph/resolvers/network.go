package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/jlentink/bekup/internal/egress"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/netguard"
)

// Runtime SSRF allowlist resolvers (D-120, D-196). The directives and the
// root-field gate check the admin role (and the step-up for the update);
// internal/egress re-checks the update.

var errEgressNotConfigured = errors.New("resolvers: SSRF allowlist service not configured")

func (r *Resolver) egress() (*egress.Service, error) {
	if r.Egress == nil {
		return nil, errEgressNotConfigured
	}
	return r.Egress, nil
}

// egressError maps service errors to API errors; entries maps the
// service's field names to the entry form of the input ("cidrs.3" →
// "cidrEntries.3.value") for the lists given that way.
func egressError(err error, entries map[string]bool) error {
	var fe *egress.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(entryField(fe.Field, entries), fe.Message)
	case errors.Is(err, egress.ErrConflict):
		return apierror.New(apierror.Conflict, "the allowlist was changed in the meantime; reload and try again")
	default:
		return err
	}
}

var entryLists = map[string]string{netguard.ListCIDRs: "cidrEntries", netguard.ListHosts: "hostEntries"}

func entryField(f string, entries map[string]bool) string {
	list, rest, _ := strings.Cut(f, ".")
	if !entries[list] {
		return f
	}
	name := entryLists[list]
	if rest == "" {
		return name
	}
	if !strings.Contains(rest, ".") {
		rest += ".value"
	}
	return name + "." + rest
}

func scopeModels(in []netguard.Scope) []model.SsrfScope {
	out := make([]model.SsrfScope, len(in))
	for i, s := range in {
		out[i] = model.SsrfScope(strings.ToUpper(string(s)))
	}
	return out
}

func scopesFromModel(in []model.SsrfScope) []netguard.Scope {
	if in == nil {
		return nil
	}
	out := make([]netguard.Scope, len(in))
	for i, s := range in {
		out[i] = netguard.Scope(strings.ToLower(string(s)))
	}
	return out
}

func entryModels(in []netguard.Entry) []model.SsrfAllowlistEntry {
	out := make([]model.SsrfAllowlistEntry, len(in))
	for i, e := range in {
		out[i] = model.SsrfAllowlistEntry{Value: e.Value, Scopes: scopeModels(e.Scopes)}
	}
	return out
}

func entryValues(in []netguard.Entry) []string {
	out := make([]string, len(in))
	for i, e := range in {
		out[i] = e.Value
	}
	return out
}

func ssrfAllowlistModel(v *egress.View) *model.SsrfAllowlist {
	return &model.SsrfAllowlist{
		Cidrs: entryValues(v.CIDRs), Hosts: entryValues(v.Hosts),
		CidrEntries: entryModels(v.CIDRs), HostEntries: entryModels(v.Hosts),
		ConfigScopes: scopeModels(netguard.DefaultScopes()), DefaultScopes: scopeModels(netguard.DefaultScopes()),
		PersonalWebhooksWidened: v.PersonalWebhooks,
		ConfigCidrs:             nonNil(v.ConfigCIDRs), ConfigHosts: nonNil(v.ConfigHosts),
		LoopbackAllowed: v.LoopbackAllowed, IgnoredEntries: v.Ignored, MaxEntries: egress.MaxEntries,
		UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedBy,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *Resolver) ssrfAllowlist(ctx context.Context) (*model.SsrfAllowlist, error) {
	svc, err := r.egress()
	if err != nil {
		return nil, err
	}
	v, err := svc.Get(ctx)
	if err != nil {
		return nil, egressError(err, nil)
	}
	return ssrfAllowlistModel(&v), nil
}

// allowlistInput takes one list from its plain or its entry form
// (exactly one must be given).
func allowlistInput(plain []string, entries []model.SsrfAllowlistEntryInput, list string) ([]netguard.Entry, bool, error) {
	switch {
	case plain != nil && entries != nil:
		return nil, false, apierror.Invalid(entryLists[list], "give either "+list+" or "+entryLists[list])
	case entries != nil:
		out := make([]netguard.Entry, len(entries))
		for i, e := range entries {
			out[i] = netguard.Entry{Value: e.Value, Scopes: scopesFromModel(e.Scopes)}
		}
		return out, true, nil
	case plain != nil:
		return egress.Values(plain), false, nil
	default:
		return nil, false, apierror.Invalid(entryLists[list], list+" or "+entryLists[list]+" is required")
	}
}

func (r *Resolver) updateSsrfAllowlist(ctx context.Context, in model.SsrfAllowlistInput) (*model.SsrfAllowlist, error) {
	svc, err := r.egress()
	if err != nil {
		return nil, err
	}
	cidrs, cidrEntries, err := allowlistInput(in.Cidrs, in.CidrEntries, netguard.ListCIDRs)
	if err != nil {
		return nil, err
	}
	hosts, hostEntries, err := allowlistInput(in.Hosts, in.HostEntries, netguard.ListHosts)
	if err != nil {
		return nil, err
	}
	entries := map[string]bool{netguard.ListCIDRs: cidrEntries, netguard.ListHosts: hostEntries}
	v, err := svc.Update(ctx, egress.Update{CIDRs: cidrs, Hosts: hosts, ExpectedUpdatedAt: in.ExpectedUpdatedAt})
	if err != nil {
		return nil, egressError(err, entries)
	}
	return ssrfAllowlistModel(&v), nil
}
