package engine

import "fmt"

// Minimum client versions per release series. A catalog entry whose client
// version is below its series floor is rejected; so is a series that is not
// listed and is older than openFrom (EOL series that never got the fix).
// Series at or above openFrom are accepted from openFrom on.
//
//   - PostgreSQL: the psql \restrict fix (CVE-2025-8714) in 13.22, 14.19,
//     15.14, 16.10, 17.6 and 18.0.
//   - MySQL: mysql --system-command (8.0.40, 8.4.3, 9.1.0) and --commands
//     (taken as 8.0.43, 8.4.6, 9.4.0; only the pinned versions were checked
//     in the images); the restore always passes both, so the floor is the
//     later of the two and 9.0-9.3 are rejected.
//   - MariaDB: mariadb --sandbox (10.5.25, 10.6.18, 10.11.8, 11.0.6, 11.1.5,
//     11.2.4, 11.4.2); 11.5+ from its first GA release 11.5.2.
//     10.7-10.10 and 11.3 never got the fix and are rejected.
//
// Verified in the pinned images on 2026-10-27: --restrict-key in pg_dump
// 13.23 and 18.6; --system-command and --commands in mysql 8.0.46, 8.4.13
// and 9.7.4; --sandbox in mariadb 10.6.28, 10.11.19, 11.4.13, 11.8.9 and
// 12.3.3. A client that lacks a flag fails closed (unknown option).
type clientFloors struct {
	series   map[Version]Version // series (patch 0; PG: minor 0) -> minimum
	openFrom Version
}

var floors = map[Kind]clientFloors{
	KindPostgres: {
		series: map[Version]Version{
			{Major: 13}: {Major: 13, Minor: 22},
			{Major: 14}: {Major: 14, Minor: 19},
			{Major: 15}: {Major: 15, Minor: 14},
			{Major: 16}: {Major: 16, Minor: 10},
			{Major: 17}: {Major: 17, Minor: 6},
		},
		openFrom: Version{Major: 18},
	},
	KindMySQL: {
		series: map[Version]Version{
			{Major: 8, Minor: 0}: {Major: 8, Minor: 0, Patch: 43},
			{Major: 8, Minor: 4}: {Major: 8, Minor: 4, Patch: 6},
		},
		openFrom: Version{Major: 9, Minor: 4},
	},
	KindMariaDB: {
		series: map[Version]Version{
			{Major: 10, Minor: 5}:  {Major: 10, Minor: 5, Patch: 25},
			{Major: 10, Minor: 6}:  {Major: 10, Minor: 6, Patch: 18},
			{Major: 10, Minor: 11}: {Major: 10, Minor: 11, Patch: 8},
			{Major: 11, Minor: 0}:  {Major: 11, Minor: 0, Patch: 6},
			{Major: 11, Minor: 1}:  {Major: 11, Minor: 1, Patch: 5},
			{Major: 11, Minor: 2}:  {Major: 11, Minor: 2, Patch: 4},
			{Major: 11, Minor: 4}:  {Major: 11, Minor: 4, Patch: 2},
		},
		openFrom: Version{Major: 11, Minor: 5, Patch: 2},
	},
}

// seriesOf returns the release series of v for engine k: the major version
// for PostgreSQL 10+, major.minor otherwise.
func seriesOf(k Kind, v Version) Version {
	if k == KindPostgres && v.Major >= 10 {
		return Version{Major: v.Major}
	}
	return Version{Major: v.Major, Minor: v.Minor}
}

// checkFloor returns an error if client version v of engine k is below the
// minimum for its series.
func checkFloor(k Kind, v Version) error {
	f, ok := floors[k]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, k)
	}
	if v.Compare(f.openFrom) >= 0 {
		return nil
	}
	minimum, ok := f.series[seriesOf(k, v)]
	if !ok {
		return fmt.Errorf("%w: %s client series of %s is not supported", ErrInvalidCatalog, k, v)
	}
	if v.Less(minimum) {
		return fmt.Errorf("%w: %s client %s is below the minimum %s", ErrInvalidCatalog, k, v, minimum)
	}
	return nil
}

// Minimum supported server versions (dump and restore targets).
var minServer = map[Kind]Version{
	KindPostgres: {Major: 10},
	KindMySQL:    {Major: 8, Minor: 0},
	KindMariaDB:  {Major: 10, Minor: 4},
}

// MinServerVersion returns the oldest supported server version of k.
func MinServerVersion(k Kind) (Version, bool) {
	v, ok := minServer[k]
	return v, ok
}
