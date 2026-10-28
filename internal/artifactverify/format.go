package artifactverify

import (
	"errors"
	"strings"
)

// ErrArtifactName reports a stored object name without a known format.
var ErrArtifactName = errors.New("artifactverify: unknown artifact file format")

// StoredFormat parses the file name of a stored object
// ("<db>.dump.zst.age", "globals.globals.sql.zst", ...) into the dump
// extension ("dump" or "sql") and the stored extension ("dump.zst.age",
// ...). encrypted says whether the object must end in ".age".
func StoredFormat(objectKey string, encrypted bool) (plainExt, storedExt string, err error) {
	name := objectKey
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	rest := name
	if encrypted {
		var ok bool
		if rest, ok = strings.CutSuffix(rest, ".age"); !ok {
			return "", "", ErrArtifactName
		}
	} else if strings.HasSuffix(rest, ".age") {
		return "", "", ErrArtifactName
	}
	rest, ok := strings.CutSuffix(rest, ".zst")
	if !ok {
		return "", "", ErrArtifactName
	}
	switch {
	case strings.HasSuffix(rest, ".dump"):
		plainExt = "dump"
	case strings.HasSuffix(rest, ".sql"):
		plainExt = "sql"
	default:
		return "", "", ErrArtifactName
	}
	storedExt = plainExt + ".zst"
	if encrypted {
		storedExt += ".age"
	}
	return plainExt, storedExt, nil
}
