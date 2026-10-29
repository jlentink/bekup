package egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pubsub"
)

// SettingKey is the settings key of the admin allowlist; it is also the
// payload of its pubsub.SettingsEvents notification.
const SettingKey = "netguard.allowlist"

// MaxEntries caps each admin list (CIDRs and host names).
const MaxEntries = 256

// maxEntryBytes caps one entry before it is parsed (a host name has at
// most 253 bytes, a CIDR far less).
const maxEntryBytes = 260

// Errors.
var (
	// ErrConflict: the list changed since the caller read it.
	ErrConflict = errors.New("egress: the allowlist was changed in the meantime")
)

// FieldError is a rejected input; Field is "cidrs.<i>", "hosts.<i>",
// "cidrs" or "hosts".
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return "egress: " + e.Field + ": " + e.Message }

// DB is what the service needs (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Options configure New.
type Options struct {
	DB      DB
	Auditor *audit.Auditor
	// Policy re-checks the caller of Update (admin, fresh step-up).
	Policy *authz.Policy
	// Guards is the process's set of per-scope SSRF guards (D-198).
	Guards *netguard.Set
	// Publisher announces changes to the other processes (nil: none,
	// e.g. tests).
	Publisher *pubsub.Publisher
	// ConfigCIDRs and ConfigHosts are the bootstrap list of [netguard]
	// (shown read-only; the guard already applies them).
	ConfigCIDRs []string
	ConfigHosts []string
	Logger      *slog.Logger
}

// Service reads, changes and applies the admin allowlist. It is safe for
// concurrent use.
type Service struct {
	o Options
	// mu serializes applying a list to the guard.
	mu sync.Mutex
	// config is the bootstrap list shown read-only; a config reload
	// replaces it (SetConfigList, D-200).
	config atomic.Pointer[configList]
}

type configList struct{ cidrs, hosts []string }

// New validates o.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Guards == nil {
		return nil, errors.New("egress: DB and Guards are required")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	svc := &Service{o: o}
	svc.SetConfigList(o.ConfigCIDRs, o.ConfigHosts)
	return svc, nil
}

// SetConfigList replaces the bootstrap list the admin page shows after a
// config reload applied it to the guards (D-200).
func (s *Service) SetConfigList(cidrs, hosts []string) {
	s.config.Store(&configList{cidrs: slices.Clone(cidrs), hosts: slices.Clone(hosts)})
}

// View is the allowlist as the admin page shows it.
type View struct {
	// CIDRs and Hosts are the admin list (canonical forms, each with its
	// scopes).
	CIDRs []netguard.Entry
	Hosts []netguard.Entry
	// ConfigCIDRs and ConfigHosts are the bootstrap list from bekup.toml;
	// it applies to netguard.DefaultScopes (never personal webhooks).
	ConfigCIDRs []string
	ConfigHosts []string
	// PersonalWebhooks is set when an admin entry carries the
	// personal_webhooks scope (a weakened default: users' personal
	// webhooks may reach private targets).
	PersonalWebhooks bool
	// LoopbackAllowed is set in dev mode (loopback may be allowlisted).
	LoopbackAllowed bool
	// Ignored counts stored entries that this process rejects (e.g.
	// loopback after leaving dev mode); they are not applied.
	Ignored   int
	UpdatedAt *time.Time
	UpdatedBy *uuid.UUID
}

// storedVersion is the version of the stored list: 2 adds scopes; a
// version-1 list (plain strings) is read with netguard.DefaultScopes
// (migration 00031 converts it).
const storedVersion = 2

type storedEntry struct {
	Value  string   `json:"value"`
	Scopes []string `json:"scopes"`
}

type storedList struct {
	Version int           `json:"version"`
	CIDRs   []storedEntry `json:"cidrs"`
	Hosts   []storedEntry `json:"hosts"`
}

type storedListV1 struct {
	CIDRs []string `json:"cidrs"`
	Hosts []string `json:"hosts"`
}

func emptyList() storedList {
	return storedList{Version: storedVersion, CIDRs: []storedEntry{}, Hosts: []storedEntry{}}
}

func toEntries(in []storedEntry) []netguard.Entry {
	out := make([]netguard.Entry, len(in))
	for i, e := range in {
		sc := make([]netguard.Scope, len(e.Scopes))
		for j, v := range e.Scopes {
			sc[j] = netguard.Scope(v)
		}
		// A stored entry without scopes is invalid (rejected, not
		// defaulted): an empty slice, never nil.
		out[i] = netguard.Entry{Value: e.Value, Scopes: sc}
	}
	return out
}

func fromEntries(in []netguard.Entry) []storedEntry {
	out := make([]storedEntry, len(in))
	for i, e := range in {
		sc := make([]string, len(e.Scopes))
		for j, v := range e.Scopes {
			sc[j] = string(v)
		}
		out[i] = storedEntry{Value: e.Value, Scopes: sc}
	}
	return out
}

func defaultEntries(values []string) []storedEntry {
	out := make([]storedEntry, len(values))
	def := make([]string, 0, len(netguard.DefaultScopes()))
	for _, sc := range netguard.DefaultScopes() {
		def = append(def, string(sc))
	}
	for i, v := range values {
		out[i] = storedEntry{Value: v, Scopes: slices.Clone(def)}
	}
	return out
}

type stored struct {
	list      storedList
	updatedAt *time.Time
	updatedBy *uuid.UUID
	// corrupt is set when the stored value cannot be decoded: it is
	// treated as an empty list (fail closed) until an admin saves a new
	// one.
	corrupt bool
}

func decode(row sqlc.Setting, err error) (stored, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return stored{list: emptyList()}, nil
	}
	if err != nil {
		return stored{}, fmt.Errorf("egress: read allowlist: %w", err)
	}
	at := row.UpdatedAt
	l, ok := decodeList(row.Value)
	return stored{list: l, updatedAt: &at, updatedBy: row.UpdatedBy, corrupt: !ok}, nil
}

// decodeList decodes a stored list; an undecodable value is an empty list
// (ok false). A version-1 list gets netguard.DefaultScopes per entry.
func decodeList(raw []byte) (storedList, bool) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return emptyList(), false
	}
	switch head.Version {
	case 1:
		var v1 storedListV1
		if err := json.Unmarshal(raw, &v1); err != nil {
			return emptyList(), false
		}
		return storedList{Version: storedVersion, CIDRs: defaultEntries(v1.CIDRs), Hosts: defaultEntries(v1.Hosts)}, true
	case storedVersion:
		var l storedList
		if err := json.Unmarshal(raw, &l); err != nil {
			return emptyList(), false
		}
		if l.CIDRs == nil {
			l.CIDRs = []storedEntry{}
		}
		if l.Hosts == nil {
			l.Hosts = []storedEntry{}
		}
		for _, e := range append(slices.Clone(l.CIDRs), l.Hosts...) {
			if e.Scopes == nil {
				// Entries must name their scopes; a list that does not
				// is not trusted at all.
				return emptyList(), false
			}
		}
		return l, true
	default:
		return emptyList(), false
	}
}

func (s *Service) read(ctx context.Context, q *sqlc.Queries, forUpdate bool) (stored, error) {
	if forUpdate {
		return decode(q.GetSettingForUpdate(ctx, SettingKey))
	}
	return decode(q.GetSetting(ctx, SettingKey))
}

// Get returns the allowlist (admin; the GraphQL layer checks the caller).
// Ignored counts the stored entries this process does not apply (and 1
// for an undecodable stored value).
func (s *Service) Get(ctx context.Context) (View, error) {
	st, err := s.read(ctx, sqlc.New(s.o.DB), false)
	if err != nil {
		return View{}, err
	}
	c, h, errs := s.o.Guards.NormalizeEntries(toEntries(st.list.CIDRs), toEntries(st.list.Hosts))
	ignored := len(errs)
	cfg := s.config.Load()
	if st.corrupt {
		ignored++
	}
	return View{
		CIDRs: c, Hosts: h, ConfigCIDRs: slices.Clone(cfg.cidrs), ConfigHosts: slices.Clone(cfg.hosts),
		PersonalWebhooks: hasScope(c, netguard.ScopePersonalWebhooks) || hasScope(h, netguard.ScopePersonalWebhooks),
		LoopbackAllowed:  s.o.Guards.LoopbackAllowable(), Ignored: ignored,
		UpdatedAt: st.updatedAt, UpdatedBy: st.updatedBy,
	}, nil
}

func hasScope(entries []netguard.Entry, sc netguard.Scope) bool {
	for _, e := range entries {
		if slices.Contains(e.Scopes, sc) {
			return true
		}
	}
	return false
}

// Load applies the stored list to the guard. Entries this process
// rejects are left out (fail closed: the list only gets narrower) and
// logged without their values.
func (s *Service) Load(ctx context.Context) error {
	st, err := s.read(ctx, sqlc.New(s.o.DB), false)
	if err != nil {
		return err
	}
	if st.corrupt {
		s.o.Logger.WarnContext(ctx, "the stored SSRF allowlist cannot be decoded; applying only the bootstrap list")
	}
	return s.apply(ctx, st.list)
}

func (s *Service) apply(ctx context.Context, l storedList) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, h, errs := s.o.Guards.NormalizeEntries(toEntries(l.CIDRs), toEntries(l.Hosts))
	if len(errs) > 0 {
		s.o.Logger.WarnContext(ctx, "ignoring stored SSRF allowlist entries this process rejects",
			slog.Int("ignored", len(errs)))
	}
	if err := s.o.Guards.SetAllowlist(c, h); err != nil {
		return fmt.Errorf("egress: apply allowlist: %w", err)
	}
	return nil
}

// Update is an admin's new list.
type Update struct {
	// CIDRs and Hosts are the complete new lists. An entry with nil
	// Scopes keeps the scopes stored for the same value, or gets
	// netguard.DefaultScopes when it is new; an entry with scopes must
	// name at least one.
	CIDRs []netguard.Entry
	Hosts []netguard.Entry
	// ExpectedUpdatedAt is the UpdatedAt of the list the admin edited
	// (nil: none was stored); a mismatch is ErrConflict.
	ExpectedUpdatedAt *time.Time
}

// Values wraps plain values as entries without scopes (keep the stored
// scopes, defaults for new values).
func Values(values []string) []netguard.Entry {
	out := make([]netguard.Entry, len(values))
	for i, v := range values {
		out[i] = netguard.Entry{Value: v}
	}
	return out
}

// Validate checks an update without storing it (entries without scopes
// get netguard.DefaultScopes) and returns the canonical lists or the
// first FieldError ("cidrs.<i>", "cidrs.<i>.scopes", ...).
func (s *Service) Validate(in Update) (cidrs, hosts []netguard.Entry, err error) {
	switch {
	case len(in.CIDRs) > MaxEntries:
		return nil, nil, &FieldError{Field: netguard.ListCIDRs, Message: "at most " + strconv.Itoa(MaxEntries) + " CIDRs"}
	case len(in.Hosts) > MaxEntries:
		return nil, nil, &FieldError{Field: netguard.ListHosts, Message: "at most " + strconv.Itoa(MaxEntries) + " host names"}
	}
	for _, l := range []struct {
		list    string
		entries []netguard.Entry
	}{{netguard.ListCIDRs, in.CIDRs}, {netguard.ListHosts, in.Hosts}} {
		list := l.list
		for i, v := range l.entries {
			if len(v.Value) > maxEntryBytes {
				return nil, nil, &FieldError{Field: list + "." + strconv.Itoa(i), Message: "the entry is too long"}
			}
			if len(v.Scopes) > len(netguard.AllScopes())*2 {
				return nil, nil, &FieldError{Field: list + "." + strconv.Itoa(i) + ".scopes", Message: "too many scopes"}
			}
		}
	}
	cidrs, hosts, errs := s.o.Guards.NormalizeEntries(in.CIDRs, in.Hosts)
	if len(errs) > 0 {
		e := errs[0]
		f := e.List + "." + strconv.Itoa(e.Index)
		if e.Field != "" {
			f += "." + e.Field
		}
		return nil, nil, &FieldError{Field: f, Message: e.Message}
	}
	return cidrs, hosts, nil
}

// keepScopes gives every entry without scopes the scopes stored for the
// same canonical value (none stored: left nil, i.e. the defaults).
func (s *Service) keepScopes(in []netguard.Entry, list string, cur []netguard.Entry) []netguard.Entry {
	out := slices.Clone(in)
	for i := range out {
		if out[i].Scopes != nil {
			continue
		}
		v, err := s.canonical(list, out[i].Value)
		if err != nil {
			continue
		}
		for _, c := range cur {
			if c.Value == v {
				out[i].Scopes = slices.Clone(c.Scopes)
				break
			}
		}
	}
	return out
}

// canonical normalizes one value (errors are reported by Validate).
func (s *Service) canonical(list, value string) (string, error) {
	var c, h []netguard.Entry
	var errs []*netguard.EntryError
	e := []netguard.Entry{{Value: value, Scopes: netguard.DefaultScopes()}}
	if list == netguard.ListCIDRs {
		c, _, errs = s.o.Guards.NormalizeEntries(e, nil)
	} else {
		_, h, errs = s.o.Guards.NormalizeEntries(nil, e)
	}
	if len(errs) > 0 {
		return "", errs[0]
	}
	if list == netguard.ListCIDRs {
		return c[0].Value, nil
	}
	return h[0].Value, nil
}

// diff returns the entries of next not in old.
func diff(next, old []string) []string {
	out := []string{}
	for _, v := range next {
		if !slices.Contains(old, v) {
			out = append(out, v)
		}
	}
	return out
}

// Update replaces the admin list (admin with a fresh step-up, re-checked
// here). The change is audited (setting.changed with the added and
// removed entries and the scopes added to kept entries; "widened" when
// anything was added, which raises the security.system alert, and
// "personal_webhooks_widened" when personal webhooks may reach more) and
// announced to every process in the same transaction; this process
// applies it after the commit.
func (s *Service) Update(ctx context.Context, in Update) (View, error) {
	if s.o.Policy == nil || s.o.Auditor == nil {
		return View{}, errors.New("egress: Policy and Auditor are required to update")
	}
	if err := s.o.Policy.Authorize(ctx, authz.OpSettingsUpdate); err != nil {
		return View{}, err
	}
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Valid() {
		return View{}, errors.New("egress: no caller")
	}
	// Syntax first (no lock needed), then again with the stored scopes.
	if _, _, err := s.Validate(in); err != nil {
		return View{}, err
	}
	meta := auth.RequestMetaFrom(ctx)
	var next storedList
	var cidrs, hosts []netguard.Entry
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := s.read(ctx, q, true)
		if err != nil {
			return err
		}
		if !sameTime(cur.updatedAt, in.ExpectedUpdatedAt) {
			return ErrConflict
		}
		// Compare in canonical form (stored entries this process rejects
		// count as removed).
		oldC, oldH, _ := s.o.Guards.NormalizeEntries(toEntries(cur.list.CIDRs), toEntries(cur.list.Hosts))
		resolved := Update{
			CIDRs: s.keepScopes(in.CIDRs, netguard.ListCIDRs, oldC),
			Hosts: s.keepScopes(in.Hosts, netguard.ListHosts, oldH),
		}
		if cidrs, hosts, err = s.Validate(resolved); err != nil {
			return err
		}
		next = storedList{Version: storedVersion, CIDRs: fromEntries(cidrs), Hosts: fromEntries(hosts)}
		value, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("egress: encode allowlist: %w", err)
		}
		uid := p.UserID
		if _, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: SettingKey, Value: value, UpdatedBy: &uid}); err != nil {
			return fmt.Errorf("egress: store allowlist: %w", err)
		}
		dc, dh := diffEntries(cidrs, oldC), diffEntries(hosts, oldH)
		personal := slices.Contains(dc.personal, true) || slices.Contains(dh.personal, true)
		if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionSettingChanged, IP: meta.IP,
			TargetType: "setting", TargetID: SettingKey,
			Details: audit.Details{
				"setting":                   SettingKey,
				"added_cidrs":               dc.added,
				"removed_cidrs":             diff(values(oldC), values(cidrs)),
				"added_hosts":               dh.added,
				"removed_hosts":             diff(values(oldH), values(hosts)),
				"added_scopes":              mergeScopes(dc.scopes, dh.scopes),
				"widened":                   len(dc.added)+len(dh.added)+len(dc.scopes)+len(dh.scopes) > 0,
				"personal_webhooks_widened": personal,
			},
		}); err != nil {
			return err
		}
		if s.o.Publisher != nil {
			return s.o.Publisher.PublishTx(ctx, q, pubsub.SettingsEvents.Name, SettingKey)
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if err := s.apply(ctx, next); err != nil {
		return View{}, err
	}
	s.o.Logger.WarnContext(ctx, "SSRF allowlist changed", slog.String("user_id", p.UserID.String()),
		slog.Int("cidrs", len(cidrs)), slog.Int("hosts", len(hosts)))
	return s.Get(ctx)
}

func values(entries []netguard.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

// entryDiff is what an admin list adds to the previous one.
type entryDiff struct {
	// added are the new values.
	added []string
	// scopes maps kept values to the scopes they gained.
	scopes map[string][]string
	// personal is true per new value or kept value that gained
	// netguard.ScopePersonalWebhooks.
	personal []bool
}

func diffEntries(next, old []netguard.Entry) entryDiff {
	d := entryDiff{added: []string{}, scopes: map[string][]string{}}
	for _, n := range next {
		i := slices.IndexFunc(old, func(o netguard.Entry) bool { return o.Value == n.Value })
		if i < 0 {
			d.added = append(d.added, n.Value)
			d.personal = append(d.personal, slices.Contains(n.Scopes, netguard.ScopePersonalWebhooks))
			continue
		}
		var gained []string
		for _, sc := range n.Scopes {
			if !slices.Contains(old[i].Scopes, sc) {
				gained = append(gained, string(sc))
			}
		}
		if len(gained) > 0 {
			d.scopes[n.Value] = gained
			d.personal = append(d.personal, slices.Contains(gained, string(netguard.ScopePersonalWebhooks)))
		}
	}
	return d
}

// mergeScopes lists the gained scopes as [{"value", "scopes"}] sorted by
// value (CIDRs, then hosts).
func mergeScopes(cidrs, hosts map[string][]string) []map[string]any {
	out := []map[string]any{}
	for _, m := range []map[string][]string{cidrs, hosts} {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			out = append(out, map[string]any{"value": k, "scopes": m[k]})
		}
	}
	return out
}

// sameTime compares the stored and the expected UpdatedAt (both nil, or
// equal to the microsecond Postgres stores).
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond))
}
