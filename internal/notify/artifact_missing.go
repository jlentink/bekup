package notify

import (
	"time"

	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/storage"
)

// ArtifactMissingDetail is the detail of a backup file that vanished from
// its destination (artifact.missing and the security.system alert
// artifact_deleted_externally): the object key ("file"; only the file
// name when the key is longer than MaxDetailString), the run timestamp
// read from the key's run directory ("backup_time", RFC 3339) and whether
// it belonged to the newest complete backup there. Keys are bekup's own
// layout (a fixed character set, no server text); an unparsable key adds
// nothing.
func ArtifactMissingDetail(object string, newest bool) Detail {
	d := Detail{"newest_complete": newest}
	k, err := storage.ParseKey(object)
	if err != nil {
		return d
	}
	switch s := k.String(); {
	case len(s) <= MaxDetailString:
		d["file"] = s
	case len(k.Base()) <= MaxDetailString:
		d["file"] = k.Base()
	}
	if segs := k.Segments(); len(segs) >= 2 {
		if t, err := storage.ParseTimestampSegment(segs[len(segs)-2]); err == nil {
			d["backup_time"] = t.UTC().Format(time.RFC3339)
		}
	}
	return d
}

// artifactMissingBody is the sentence of a missing backup file: which
// file of which backup, on which destination, what it means for
// retention and where to look. "" when the detail names no file (events
// recorded before the detail existed).
func artifactMissingBody(t textFunc, l i18n.Locale, job, dest string, d Detail) string {
	file := str(d, "file")
	if file == "" {
		return ""
	}
	var body string
	if at, err := time.Parse(time.RFC3339, str(d, "backup_time")); err == nil {
		body = t("body.artifact.missing.at", file, job, i18n.FormatTime(l, at.UTC(), "2 Jan 2006 15:04"), dest)
	} else {
		body = t("body.artifact.missing.file", file, job, dest)
	}
	if b, ok := d["newest_complete"].(bool); ok && b {
		body += t("body.artifact.missing.newest")
	}
	return body + t("body.artifact.missing.hint")
}
