package destinations

import (
	"path"
	"strconv"
	"strings"
)

// Destination locations (D-209). Every destination owns its location: the
// unrecorded-file scan, the empty-directory pruning and retention treat
// every file there as their own. Two destinations must therefore never
// share (or nest) a location, and a destination's location must not move
// while backups are stored there or a run is writing to it.
//
// "Same location" per backend:
//
//   - disk: the path below storage.disk_root (one disk root per process);
//   - S3: the endpoint (empty or any *.amazonaws.com endpoint is AWS, whose
//     bucket names are global) and the bucket, then the prefix;
//   - SFTP: the host and port, then base path + directory; a relative base
//     path is relative to the user's home, so the user is part of the
//     location then.
//
// Two locations overlap when their scope matches and one path is equal to
// or below the other (segment-wise).

// location is a destination's place for overlap checks.
type location struct {
	// scope is everything that must match exactly.
	scope string
	// path are the segments below the scope.
	path []string
}

// splitPath turns a slash-separated path into its non-empty segments.
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// s3Scope normalizes an S3 endpoint for location comparisons.
func s3Scope(endpoint string) string {
	e := strings.ToLower(strings.TrimSpace(endpoint))
	e = strings.TrimPrefix(strings.TrimPrefix(e, "https://"), "http://")
	e = strings.TrimSuffix(e, "/")
	host := e
	if h, _, ok := strings.Cut(e, "/"); ok {
		host = h
	}
	if hostOnly, port, ok := strings.Cut(host, ":"); ok && (port == "443" || port == "80") {
		host = hostOnly
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || host == "amazonaws.com" || strings.HasSuffix(host, ".amazonaws.com") {
		return "aws"
	}
	return host
}

// locationOf returns the location of a destination configuration (exactly
// one of the configs is set).
func locationOf(disk *DiskConfig, s3 *S3Config, sftp *SFTPConfig) (location, bool) {
	switch {
	case disk != nil:
		return location{scope: "disk", path: splitPath(disk.Path)}, true
	case s3 != nil:
		return location{scope: "s3|" + s3Scope(s3.Endpoint) + "|" + strings.TrimSpace(s3.Bucket), path: splitPath(s3.Prefix)}, true
	case sftp != nil:
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(sftp.Host)), ".")
		port := sftp.Port
		if port == 0 {
			port = defaultSFTPPort
		}
		scope := "sftp|" + host + "|" + strconv.Itoa(port)
		base := strings.TrimSpace(sftp.BasePath)
		if !strings.HasPrefix(base, "/") {
			scope += "|~" + strings.TrimSpace(sftp.User)
		}
		return location{scope: scope, path: splitPath(path.Join(base, sftp.Dir))}, true
	}
	return location{}, false
}

// overlaps reports whether two locations share files: the same scope and
// one path equal to or below the other.
func (l location) overlaps(o location) bool {
	if l.scope != o.scope {
		return false
	}
	n := min(len(l.path), len(o.path))
	for i := range n {
		if l.path[i] != o.path[i] {
			return false
		}
	}
	return true
}

// locationField is the input field an overlap error points at.
func locationField(t Type) string {
	switch t {
	case TypeS3:
		return "s3.prefix"
	case TypeSFTP:
		return "sftp.basePath"
	default:
		return "disk.path"
	}
}

// locationChange returns the input field of the first location setting
// that st changes ("" when the location stays): the disk path; the S3
// endpoint, region, bucket and prefix; the SFTP host, port, user, base
// path and directory (D-209). Any of them may point the destination at
// other files.
func locationChange(cur *record, st *state) string {
	norm := func(p string) string { return strings.Trim(strings.TrimSpace(p), "/") }
	fold := func(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }
	switch {
	case cur.disk != nil && st.disk != nil:
		if norm(cur.disk.Path) != norm(st.disk.Path) {
			return "disk.path"
		}
	case cur.s3 != nil && st.s3 != nil:
		switch {
		case s3Scope(cur.s3.Endpoint) != s3Scope(st.s3.Endpoint):
			return "s3.endpoint"
		case strings.TrimSpace(cur.s3.Region) != strings.TrimSpace(st.s3.Region):
			return "s3.region"
		case strings.TrimSpace(cur.s3.Bucket) != strings.TrimSpace(st.s3.Bucket):
			return "s3.bucket"
		case norm(cur.s3.Prefix) != norm(st.s3.Prefix):
			return "s3.prefix"
		}
	case cur.sftp != nil && st.sftp != nil:
		port := func(p int) int {
			if p == 0 {
				return defaultSFTPPort
			}
			return p
		}
		switch {
		case fold(cur.sftp.Host) != fold(st.sftp.Host):
			return "sftp.host"
		case port(cur.sftp.Port) != port(st.sftp.Port):
			return "sftp.port"
		case strings.TrimSpace(cur.sftp.User) != strings.TrimSpace(st.sftp.User):
			return "sftp.user"
		case path.Clean("/"+strings.TrimSpace(cur.sftp.BasePath)) != path.Clean("/"+strings.TrimSpace(st.sftp.BasePath)) ||
			strings.HasPrefix(strings.TrimSpace(cur.sftp.BasePath), "/") != strings.HasPrefix(strings.TrimSpace(st.sftp.BasePath), "/"):
			return "sftp.basePath"
		case norm(cur.sftp.Dir) != norm(st.sftp.Dir):
			return "sftp.dir"
		}
	}
	return ""
}
