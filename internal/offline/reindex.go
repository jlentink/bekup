package offline

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// MaxScanObjects bounds the objects one reindex lists.
const MaxScanObjects = 2_000_000

// ErrTooManyObjects reports that the destination holds more objects than
// a scan accepts (narrow it with a job slug).
var ErrTooManyObjects = errors.New("offline: too many objects on the destination (narrow the scan with --job-slug)")

// DestSource reads the files of one run directory on a destination.
type DestSource struct {
	Dest storage.Destination
	Dir  storage.Key
}

// Open implements Source.
func (s DestSource) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	k, err := s.Dir.Join(name)
	if err != nil {
		return nil, err
	}
	rc, _, err := s.Dest.Get(ctx, k)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, k)
	}
	return rc, err
}

// ScannedRun is one run directory found on a destination.
type ScannedRun struct {
	// Dir is {job-slug}/{timestamp}; JobSlug and Timestamp its segments.
	Dir       storage.Key
	JobSlug   string
	Timestamp string
	// ManifestKey is set when the directory holds manifest.json.
	ManifestKey storage.Key
	// ManifestFile is the raw manifest.json (when it could be read).
	ManifestFile []byte
	// Report is set when the signature verified (the artifact checks are
	// in it).
	Report *Report
	// Err is why the run could not be verified (no manifest: incomplete
	// run, bad signature, wrong location, read error).
	Err error
	// Objects is the number of objects in the directory.
	Objects int
}

// Verified reports whether the signature verified and every artifact
// matched.
func (r *ScannedRun) Verified() bool { return r.Err == nil && r.Report != nil && r.Report.OK }

// ManifestSHA256 returns the digest of manifest.json.
func (r *ScannedRun) ManifestSHA256() []byte {
	s := sha256.Sum256(r.ManifestFile)
	return s[:]
}

// ScanOptions configure Scan.
type ScanOptions struct {
	// JobSlug limits the scan to one job's directory.
	JobSlug string
	// Keys verify the manifest signatures (required).
	Keys pipeline.KeyVerifier
	// KeySet, if set, supplies the fingerprints for the reports.
	KeySet *KeySet
	// SkipArtifacts only verifies the manifests (no artifact reads).
	SkipArtifacts bool
}

// Scan lists a destination, groups its objects into run directories
// ({job-slug}/{timestamp}/...) and verifies every manifest found: the
// signature, its location (job slug and timestamp of the directory) and,
// unless SkipArtifacts, every artifact's size and SHA-256 (streamed from
// the destination). Objects outside the layout (probes, other files) are
// ignored. The result is sorted by directory.
func Scan(ctx context.Context, dest storage.Destination, opts ScanOptions) ([]ScannedRun, error) {
	if opts.Keys == nil {
		return nil, ErrNoKeys
	}
	var prefix storage.Key
	if opts.JobSlug != "" {
		if err := storage.ValidateJobSlug(opts.JobSlug); err != nil {
			return nil, err
		}
		p, err := storage.ParsePrefix(opts.JobSlug)
		if err != nil {
			return nil, err
		}
		prefix = p
	}
	runs := map[string]*ScannedRun{}
	n := 0
	for obj, err := range storage.All(ctx, dest, prefix) {
		if err != nil {
			return nil, fmt.Errorf("list destination: %w", err)
		}
		n++
		if n > MaxScanObjects {
			return nil, ErrTooManyObjects
		}
		segs := obj.Key.Segments()
		if len(segs) != 3 || storage.ValidateJobSlug(segs[0]) != nil {
			continue
		}
		if _, err := storage.ParseTimestampSegment(segs[1]); err != nil {
			continue
		}
		dir := obj.Key.Dir()
		r := runs[dir.String()]
		if r == nil {
			r = &ScannedRun{Dir: dir, JobSlug: segs[0], Timestamp: segs[1]}
			runs[dir.String()] = r
		}
		r.Objects++
		if segs[2] == storage.ManifestFileName {
			r.ManifestKey = obj.Key
		}
	}
	out := make([]ScannedRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b ScannedRun) int {
		switch {
		case a.Dir.String() < b.Dir.String():
			return -1
		case a.Dir.String() > b.Dir.String():
			return 1
		}
		return 0
	})
	for i := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		verifyScanned(ctx, dest, &out[i], opts)
	}
	return out, nil
}

// ErrIncomplete reports a run directory without manifest.json.
var ErrIncomplete = errors.New("offline: no manifest.json (incomplete or failed run)")

func verifyScanned(ctx context.Context, dest storage.Destination, r *ScannedRun, opts ScanOptions) {
	if r.ManifestKey.IsZero() {
		r.Err = ErrIncomplete
		return
	}
	src := DestSource{Dest: dest, Dir: r.Dir}
	data, err := ReadManifest(ctx, src)
	if err != nil {
		r.Err = fmt.Errorf("read manifest: %w", err)
		return
	}
	r.ManifestFile = data
	m, err := VerifyManifest(data, opts.Keys)
	if err != nil {
		r.Err = err
		return
	}
	if err := CheckLocation(m, r.JobSlug, r.Timestamp); err != nil {
		r.Err = err
		return
	}
	vo := VerifyOptions{}
	if opts.SkipArtifacts {
		vo.Only = map[string]bool{}
	}
	r.Report = VerifyRun(ctx, data, m, src, opts.KeySet, vo)
}
