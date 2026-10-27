package engine

import "fmt"

// WarningCode identifies a compatibility warning.
type WarningCode string

// Compatibility warnings (shown in the UI and the run log).
const (
	// WarnClientOlderThanServer: no MySQL/MariaDB client of the server's
	// series or newer exists in the catalog; the newest older one is used.
	WarnClientOlderThanServer WarningCode = "client_older_than_server"
	// WarnLegacyClient: the selected client series is end of life.
	WarnLegacyClient WarningCode = "legacy_client"
)

// Warning is a non-fatal compatibility finding.
type Warning struct {
	Code    WarningCode
	Message string
}

// SelectDumpClient picks the client image for dumping a server of version
// server:
//
//   - PostgreSQL: the lowest client major >= the server major. A server
//     newer than every client is an error (D-036: block, not warn).
//   - MySQL/MariaDB: the server's series if present, else the lowest newer
//     series, else the newest older series with WarnClientOlderThanServer.
func (c *Catalog) SelectDumpClient(k Kind, server Version) (Entry, []Warning, error) {
	return c.selectClient(k, server)
}

// SelectRestoreClient picks the client image for restoring a dump made with
// client version dumpClient into a server of version target: the same rule
// as SelectDumpClient applied to max(dumpClient, target), so a PostgreSQL
// archive is never read by an older pg_restore.
func (c *Catalog) SelectRestoreClient(k Kind, dumpClient, target Version) (Entry, []Warning, error) {
	if err := checkServer(k, target); err != nil {
		return Entry{}, nil, err
	}
	need := target
	if seriesOf(k, dumpClient).Compare(seriesOf(k, target)) > 0 {
		need = dumpClient
	}
	return c.selectClient(k, need)
}

func checkServer(k Kind, server Version) error {
	minimum, ok := minServer[k]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, truncate(string(k)))
	}
	if server.Less(minimum) {
		return fmt.Errorf("%w: %s %s is older than %s", ErrServerTooOld, k, server, minimum)
	}
	return nil
}

func (c *Catalog) selectClient(k Kind, server Version) (Entry, []Warning, error) {
	if err := checkServer(k, server); err != nil {
		return Entry{}, nil, err
	}
	want := seriesOf(k, server)
	entries := c.EntriesFor(k) // oldest first
	var chosen Entry
	var warns []Warning
	for i := range entries {
		if seriesOf(k, entries[i].version).Compare(want) >= 0 {
			chosen = entries[i]
			break
		}
	}
	if chosen.IsZero() {
		if k == KindPostgres || len(entries) == 0 {
			return Entry{}, nil, fmt.Errorf("%w: %s server %s", ErrNoClient, k, server)
		}
		chosen = entries[len(entries)-1]
		warns = append(warns, Warning{
			Code:    WarnClientOlderThanServer,
			Message: fmt.Sprintf("no %s client for server %s; using the older client %s", k, server, chosen.version),
		})
	}
	if chosen.legacy {
		warns = append(warns, Warning{
			Code:    WarnLegacyClient,
			Message: fmt.Sprintf("%s client series %s is end of life", k, chosen.key),
		})
	}
	return chosen, warns, nil
}

// CheckDumpCompat checks an explicitly chosen client entry (a connection's
// image key) against a server version with the same rules.
func CheckDumpCompat(e Entry, server Version) ([]Warning, error) {
	if e.IsZero() {
		return nil, ErrInvalidEntry
	}
	if err := checkServer(e.kind, server); err != nil {
		return nil, err
	}
	var warns []Warning
	if seriesOf(e.kind, e.version).Less(seriesOf(e.kind, server)) {
		if e.kind == KindPostgres {
			return nil, fmt.Errorf("%w: pg_dump %s cannot dump server %s", ErrNoClient, e.version, server)
		}
		warns = append(warns, Warning{
			Code:    WarnClientOlderThanServer,
			Message: fmt.Sprintf("%s client %s is older than server %s", e.kind, e.version, server),
		})
	}
	if e.legacy {
		warns = append(warns, Warning{
			Code:    WarnLegacyClient,
			Message: fmt.Sprintf("%s client series %s is end of life", e.kind, e.key),
		})
	}
	return warns, nil
}
