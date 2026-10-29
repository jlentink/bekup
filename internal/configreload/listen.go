package configreload

import (
	"strings"

	"github.com/google/uuid"
)

// Prefixes of the application_name of the LISTEN connections that receive
// reload broadcasts (serve and `bekup worker`). The full name carries the
// process's instance id ("<prefix>:<uuid>", 56 characters, within
// Postgres's 63-byte limit), so the status page can tell which processes
// listen right now. application_name is chosen by the client: anything
// that can log in to the app database can claim one, so it is used for
// display and progress only, never for a security decision (D-200).
const (
	ServeListenerPrefix  = "bekup-serve-listen"
	WorkerListenerPrefix = "bekup-worker-listen"
)

// ListenerApplicationName is the application_name of kind's LISTEN
// connection.
func ListenerApplicationName(kind ProcessKind, id uuid.UUID) string {
	prefix := ServeListenerPrefix
	if kind == KindWorker {
		prefix = WorkerListenerPrefix
	}
	return prefix + ":" + id.String()
}

// ParseListenerApplicationName returns the instance id of a listener's
// application_name (false for any other shape).
func ParseListenerApplicationName(name string) (uuid.UUID, bool) {
	for _, prefix := range []string{ServeListenerPrefix, WorkerListenerPrefix} {
		rest, ok := strings.CutPrefix(name, prefix+":")
		if !ok {
			continue
		}
		id, err := uuid.Parse(rest)
		if err != nil || id.String() != rest {
			return uuid.Nil, false
		}
		return id, true
	}
	return uuid.Nil, false
}
