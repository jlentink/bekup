// Package engine holds the image catalog (catalog.toml, digest-pinned
// multi-arch images), version-compatibility rules and pure argv builders for
// the PostgreSQL, MySQL and MariaDB native dump and restore tools.
//
// Everything here is pure: no Docker, no network, no secrets in argv. The
// runner (task 2.5) looks up an Entry in the Catalog (embedded catalog plus
// the optional extra-images file), validates the request, asks the Engine
// for a Command and writes the Command's Files (password file, pinned CA,
// client certificate) through the stdin preamble before exec (D-031, D-032,
// D-033, D-036).
//
// Builders validate every name (strict charset, no leading "-"), never put
// a raw database name or URI where a tool could read it as options or a
// connection string, and pin each client to the hardened flags of its
// engine: pg_dump/pg_restore with a quoted conninfo, mysql with
// --system-command=OFF --commands=OFF --local-infile=0, mariadb with
// --sandbox --local-infile=0, and --defaults-file as the first option.
package engine
