package engine

import (
	"bytes"
	_ "embed" // catalog.toml
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

//go:embed catalog.toml
var embeddedCatalog []byte

// catalogSchemaVersion is the only supported schema_version.
const catalogSchemaVersion = 1

// maxCatalogBytes bounds an extra-images file.
const maxCatalogBytes = 1 << 20

// maxCatalogEntries bounds the number of entries in one file.
const maxCatalogEntries = 256

// Binaries are the client tool names inside an image. Each engine allows
// exactly the names in allowedBinaries.
type Binaries struct {
	Dump        string
	DumpGlobals string
	Restore     string
	Client      string
}

// Entry is one allowlisted client image. Its fields are unexported so that
// an Entry can only come from a validated Catalog.
type Entry struct {
	kind     Kind
	key      string
	image    string
	repo     string
	tag      string
	digest   string
	version  Version
	legacy   bool
	extra    bool
	binaries Binaries
	uid      int
}

// Kind returns the engine.
func (e Entry) Kind() Kind { return e.kind }

// Key returns the catalog key: the release series ("18", "8.4").
func (e Entry) Key() string { return e.key }

// Image returns the full digest-pinned reference "repo:tag@sha256:...".
func (e Entry) Image() string { return e.image }

// Repository returns the image repository ("postgres").
func (e Entry) Repository() string { return e.repo }

// Tag returns the image tag ("18.6-alpine").
func (e Entry) Tag() string { return e.tag }

// Digest returns the multi-arch index digest ("sha256:...").
func (e Entry) Digest() string { return e.digest }

// ClientVersion returns the client version derived from the tag.
func (e Entry) ClientVersion() Version { return e.version }

// Legacy reports whether the series is end of life (shown in the UI).
func (e Entry) Legacy() bool { return e.legacy }

// Extra reports whether the entry comes from the operator's extra file.
func (e Entry) Extra() bool { return e.extra }

// Binaries returns the client tool names.
func (e Entry) Binaries() Binaries { return e.binaries }

// UID returns the non-root user and group ID the image's client tools run
// as (the image's own database user: 70 in the official alpine PostgreSQL
// images, 999 in the official MySQL and MariaDB images). Never 0.
func (e Entry) UID() int { return e.uid }

// IsZero reports whether e is the zero Entry (not from a catalog).
func (e Entry) IsZero() bool { return e.image == "" }

// Catalog is an immutable, validated set of allowlisted client images.
type Catalog struct {
	entries []Entry // sorted by kind, then series
}

// catalogFile is the TOML schema of catalog.toml and the extra file.
type catalogFile struct {
	SchemaVersion int            `toml:"schema_version"`
	Images        []catalogImage `toml:"images"`
}

type catalogImage struct {
	Engine   string          `toml:"engine"`
	Key      string          `toml:"key"`
	Image    string          `toml:"image"`
	Legacy   bool            `toml:"legacy"`
	Binaries catalogBinaries `toml:"binaries"`
	// UID is optional: 0 selects the engine's default (defaultUIDs).
	UID int `toml:"uid"`
}

type catalogBinaries struct {
	Dump        string `toml:"dump"`
	DumpGlobals string `toml:"dump_globals"`
	Restore     string `toml:"restore"`
	Client      string `toml:"client"`
}

// allowedBinaries are the only tool names per engine (empty = not used).
var allowedBinaries = map[Kind]Binaries{
	KindPostgres: {Dump: "pg_dump", DumpGlobals: "pg_dumpall", Restore: "pg_restore", Client: "psql"},
	KindMySQL:    {Dump: "mysqldump", Client: "mysql"},
	KindMariaDB:  {Dump: "mariadb-dump", Client: "mariadb"},
}

// defaultUIDs are the non-root users of the official images: postgres in
// the alpine PostgreSQL images, mysql in the MySQL and MariaDB images
// (verified with `id -u` in every pinned image). Extra entries for other
// images set `uid` explicitly.
var defaultUIDs = map[Kind]int{KindPostgres: 70, KindMySQL: 999, KindMariaDB: 999}

// maxUID bounds the uid key (no "nobody"-overflow or negative values).
const maxUID = 1<<31 - 2

var (
	// imageRe: [registry[:port]/]path:tag@sha256:<64 hex>. The digest is
	// mandatory; it must be the multi-arch index digest.
	imageRe = regexp.MustCompile(`^((?:[a-z0-9][a-z0-9.-]*(?::[0-9]{1,5})?/)?[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*):([A-Za-z0-9_][A-Za-z0-9_.-]{0,127})@(sha256:[a-f0-9]{64})$`)
	// tagVersionRe: the tag starts with the client version.
	tagVersionRe = regexp.MustCompile(`^([0-9]{1,5}\.[0-9]{1,5}(?:\.[0-9]{1,5})?)(?:-[A-Za-z0-9_.-]+)?$`)
)

var embedded = sync.OnceValues(func() (*Catalog, error) {
	return parse(embeddedCatalog, false)
})

// Embedded returns the catalog compiled into the binary.
func Embedded() (*Catalog, error) { return embedded() }

// Parse parses and validates a catalog file. Embedded-catalog rules apply
// (official repositories only).
func Parse(data []byte) (*Catalog, error) { return parse(data, false) }

// WithExtra returns a new catalog with the entries of an operator-supplied
// extra-images file added. Extra entries follow the same schema and rules
// except that any repository is allowed; they cannot replace an embedded
// key.
func (c *Catalog) WithExtra(data []byte) (*Catalog, error) {
	extra, err := parse(data, true)
	if err != nil {
		return nil, fmt.Errorf("extra images: %w", err)
	}
	merged := slices.Clone(c.entries)
	for i := range extra.entries {
		e := &extra.entries[i]
		if _, ok := c.Lookup(e.kind, e.key); ok {
			return nil, fmt.Errorf("%w: extra images: %s %s overrides an embedded entry", ErrInvalidCatalog, e.kind, e.key)
		}
		merged = append(merged, *e)
	}
	sortEntries(merged)
	return &Catalog{entries: merged}, nil
}

func parse(data []byte, extra bool) (*Catalog, error) {
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("%w: file larger than %d bytes", ErrInvalidCatalog, maxCatalogBytes)
	}
	var f catalogFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	if f.SchemaVersion != catalogSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d, want %d", ErrInvalidCatalog, f.SchemaVersion, catalogSchemaVersion)
	}
	if len(f.Images) == 0 && !extra {
		return nil, fmt.Errorf("%w: no images", ErrInvalidCatalog)
	}
	if len(f.Images) > maxCatalogEntries {
		return nil, fmt.Errorf("%w: more than %d images", ErrInvalidCatalog, maxCatalogEntries)
	}
	entries := make([]Entry, 0, len(f.Images))
	seen := make(map[string]bool, len(f.Images))
	for i := range f.Images {
		e, err := f.Images[i].entry(extra)
		if err != nil {
			return nil, fmt.Errorf("%w: images[%d]: %w", ErrInvalidCatalog, i, err)
		}
		id := string(e.kind) + "/" + e.key
		if seen[id] {
			return nil, fmt.Errorf("%w: images[%d]: duplicate key %s %s", ErrInvalidCatalog, i, e.kind, e.key)
		}
		seen[id] = true
		entries = append(entries, e)
	}
	sortEntries(entries)
	return &Catalog{entries: entries}, nil
}

func (img catalogImage) entry(extra bool) (Entry, error) {
	k := Kind(img.Engine)
	if !k.Valid() {
		return Entry{}, fmt.Errorf("unknown engine %q", truncate(img.Engine))
	}
	m := imageRe.FindStringSubmatch(img.Image)
	if m == nil {
		return Entry{}, fmt.Errorf("image %q is not repo:tag@sha256:<digest>", truncate(img.Image))
	}
	repo, tag, digest := m[1], m[2], m[3]
	if !extra && repo != string(k) {
		return Entry{}, fmt.Errorf("embedded image %q is not the official %s image", truncate(img.Image), k)
	}
	tv := tagVersionRe.FindStringSubmatch(tag)
	if tv == nil {
		return Entry{}, fmt.Errorf("tag %q does not start with a version", tag)
	}
	v, err := ParseVersion(tv[1])
	if err != nil {
		return Entry{}, err
	}
	if k != KindPostgres && strings.Count(tv[1], ".") != 2 {
		return Entry{}, fmt.Errorf("tag %q needs a full major.minor.patch version", tag)
	}
	if k == KindPostgres && v.Major < 10 {
		return Entry{}, fmt.Errorf("tag %q: PostgreSQL clients before 10 are not supported", tag)
	}
	if want := seriesKey(k, v); img.Key != want {
		return Entry{}, fmt.Errorf("key %q does not match the tag's series %q", truncate(img.Key), want)
	}
	if err := checkFloor(k, v); err != nil {
		return Entry{}, err
	}
	b := Binaries(img.Binaries)
	if b != allowedBinaries[k] {
		return Entry{}, fmt.Errorf("binaries %+v not allowed for %s (want %+v)", b, k, allowedBinaries[k])
	}
	uid := img.UID
	switch {
	case uid == 0:
		uid = defaultUIDs[k]
	case uid < 0 || uid > maxUID:
		return Entry{}, fmt.Errorf("uid %d out of range (1..%d; never root)", img.UID, maxUID)
	}
	return Entry{
		kind: k, key: img.Key, image: img.Image, repo: repo, tag: tag, digest: digest,
		version: v, legacy: img.Legacy, extra: extra, binaries: b, uid: uid,
	}, nil
}

// seriesKey formats the catalog key of version v.
func seriesKey(k Kind, v Version) string {
	if k == KindPostgres {
		return strconv.Itoa(v.Major)
	}
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

func sortEntries(es []Entry) {
	slices.SortFunc(es, func(a, b Entry) int {
		if c := strings.Compare(string(a.kind), string(b.kind)); c != 0 {
			return c
		}
		return a.version.Compare(b.version)
	})
}

// Entries returns all entries sorted by engine and version.
func (c *Catalog) Entries() []Entry { return slices.Clone(c.entries) }

// EntriesFor returns the entries of one engine, oldest first.
func (c *Catalog) EntriesFor(k Kind) []Entry {
	var out []Entry
	for i := range c.entries {
		if c.entries[i].kind == k {
			out = append(out, c.entries[i])
		}
	}
	return out
}

// Lookup returns the entry for an engine and key.
func (c *Catalog) Lookup(k Kind, key string) (Entry, bool) {
	for i := range c.entries {
		if c.entries[i].kind == k && c.entries[i].key == key {
			return c.entries[i], true
		}
	}
	return Entry{}, false
}

// Images returns the image allowlist: every distinct image reference.
func (c *Catalog) Images() []string {
	out := make([]string, 0, len(c.entries))
	for i := range c.entries {
		out = append(out, c.entries[i].image)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// AllowsImage reports whether ref is exactly an allowlisted image.
func (c *Catalog) AllowsImage(ref string) bool {
	return slices.ContainsFunc(c.entries, func(e Entry) bool { return e.image == ref })
}
