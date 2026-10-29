package netguard

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/config"
)

// Scope is an outbound use an allowlist entry widens (D-198). Every use
// dials through its own Guard of a Set, which honors only the entries
// with its scope.
type Scope string

// Scopes.
const (
	// ScopeDestinations: S3 and SFTP destinations (admin-configured).
	ScopeDestinations Scope = "destinations"
	// ScopeSMTP: the SMTP server (admin-configured).
	ScopeSMTP Scope = "smtp"
	// ScopeSharedWebhooks: shared notification webhooks (admin-configured).
	ScopeSharedWebhooks Scope = "shared_webhooks"
	// ScopePersonalWebhooks: personal webhooks, entered by any user. Never
	// granted by default: an admin must opt in per entry.
	ScopePersonalWebhooks Scope = "personal_webhooks"
)

// AllScopes returns every scope in display order.
func AllScopes() []Scope {
	return []Scope{ScopeDestinations, ScopeSMTP, ScopeSharedWebhooks, ScopePersonalWebhooks}
}

// DefaultScopes returns the scopes of an entry that names none (new admin
// entries without scopes, stored version-1 lists, the bootstrap list):
// every scope except ScopePersonalWebhooks.
func DefaultScopes() []Scope {
	return []Scope{ScopeDestinations, ScopeSMTP, ScopeSharedWebhooks}
}

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { return slices.Contains(AllScopes(), s) }

// CanonicalScopes validates scopes and returns them deduplicated in
// AllScopes order; an empty or unknown scope is an error.
func CanonicalScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	for _, s := range scopes {
		if !s.Valid() {
			return nil, fmt.Errorf("unknown scope %q", truncate(string(s), 40))
		}
	}
	out := []Scope{}
	for _, s := range AllScopes() {
		if slices.Contains(scopes, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Entry is one allowlist entry with the scopes it applies to.
type Entry struct {
	Value  string
	Scopes []Scope
}

// Set holds one Guard per scope (one Set per process). The bootstrap list
// applies to DefaultScopes only; SetAllowlist replaces the runtime list of
// every guard together.
type Set struct {
	guards       map[Scope]*Guard
	loopbackSoft bool
}

// NewSet validates the bootstrap list of opts and returns a Set; the
// guard of ScopePersonalWebhooks starts without any allowlist.
func NewSet(opts Options) (*Set, error) {
	s := &Set{guards: map[Scope]*Guard{}, loopbackSoft: opts.LoopbackAllowable}
	personal := opts
	personal.AllowCIDRs, personal.AllowHosts = nil, nil
	for _, sc := range AllScopes() {
		o := opts
		if !slices.Contains(DefaultScopes(), sc) {
			o = personal
		}
		g, err := New(o)
		if err != nil {
			return nil, err
		}
		s.guards[sc] = g
	}
	return s, nil
}

// SetFromConfig builds a Set from [netguard]; loopback is allowlistable
// only in dev mode.
func SetFromConfig(c *config.Bekup) (*Set, error) {
	return NewSet(Options{
		AllowCIDRs:        c.Netguard.AllowCIDRs,
		AllowHosts:        c.Netguard.AllowHosts,
		LoopbackAllowable: c.Dev.Enabled,
	})
}

// Guard returns the guard of scope (nil for an unknown scope).
func (s *Set) Guard(scope Scope) *Guard { return s.guards[scope] }

// PrepareBootstrap validates a new bootstrap list ([netguard] allow_cidrs
// and allow_hosts, a config reload, D-200) with the dev mode the Set was
// built with and returns the commit that applies it to every scope except
// ScopePersonalWebhooks, whose bootstrap list always stays empty (D-198).
// The commit only does atomic stores; nothing changes on error.
func (s *Set) PrepareBootstrap(cidrs, hosts []string) (func(), error) {
	a, err := buildAllowlist(cidrs, hosts, s.loopbackSoft)
	if err != nil {
		return nil, err
	}
	return func() {
		for _, sc := range DefaultScopes() {
			s.guards[sc].bootstrap.Store(a)
		}
	}, nil
}

// LoopbackAllowable reports whether loopback may be allowlisted (dev mode).
func (s *Set) LoopbackAllowable() bool { return s.loopbackSoft }

// NormalizeEntries validates entries like SetAllowlist and returns their
// canonical forms (values as Guard.NormalizeAllowlist; scopes
// canonicalized, nil scopes meaning DefaultScopes), in input order;
// duplicates are merged (union of scopes). errs has one EntryError per
// rejected entry (List: ListCIDRs or ListHosts; a scope problem has
// Field "scopes").
func (s *Set) NormalizeEntries(cidrs, hosts []Entry) (outCIDRs, outHosts []Entry, errs []*EntryError) {
	g := s.guards[ScopeDestinations]
	norm := func(list string, in []Entry) []Entry {
		out := []Entry{}
		idx := map[string]int{}
		for i, e := range in {
			v, err := g.normalizeValue(list, e.Value)
			if err != nil {
				errs = append(errs, &EntryError{List: list, Index: i, Message: err.Error()})
				continue
			}
			scopes := e.Scopes
			if scopes == nil {
				scopes = DefaultScopes()
			}
			cs, err := CanonicalScopes(scopes)
			if err != nil {
				errs = append(errs, &EntryError{List: list, Index: i, Field: "scopes", Message: err.Error()})
				continue
			}
			if j, ok := idx[v]; ok {
				// Both sides are canonical already: the union cannot fail.
				if merged, err := CanonicalScopes(append(slices.Clone(out[j].Scopes), cs...)); err == nil {
					out[j].Scopes = merged
				}
				continue
			}
			idx[v] = len(out)
			out = append(out, Entry{Value: v, Scopes: cs})
		}
		return out
	}
	outCIDRs = norm(ListCIDRs, cidrs)
	outHosts = norm(ListHosts, hosts)
	return outCIDRs, outHosts, errs
}

// SetAllowlist validates the runtime (admin) list and applies to each
// scope's guard the entries carrying that scope. Nothing changes on
// error.
func (s *Set) SetAllowlist(cidrs, hosts []Entry) error {
	c, h, errs := s.NormalizeEntries(cidrs, hosts)
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("netguard: invalid allowlist: %s", strings.Join(msgs, "; "))
	}
	lists := map[Scope]*allowlist{}
	for _, sc := range AllScopes() {
		a, err := buildAllowlist(valuesFor(c, sc), valuesFor(h, sc), s.loopbackSoft)
		if err != nil {
			return err
		}
		lists[sc] = a
	}
	for sc, a := range lists {
		s.guards[sc].runtime.Store(a)
	}
	return nil
}

func valuesFor(entries []Entry, sc Scope) []string {
	out := []string{}
	for _, e := range entries {
		if slices.Contains(e.Scopes, sc) {
			out = append(out, e.Value)
		}
	}
	return out
}
