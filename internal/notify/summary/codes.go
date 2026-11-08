package summary

import "github.com/jlentink/bekup/internal/i18n"

// failureText is the short, fixed text of a run error code
// (runs.error_code: the jobs.Code* codes and the runner's normalized
// reasons, lower-cased) shown next to a failed job in the summary, in l:
// the "failure.<code>" texts of the catalog (texts.go). They are bekup's
// own texts, never server output; an unknown code gives "" (the note then
// says only that the backup failed). TestFailureTextsCoverCodes keeps the
// list complete in every language.
func failureText(l i18n.Locale, code string) string {
	if _, ok := texts[i18n.EN][failureKey(code)]; !ok || code == "" {
		return ""
	}
	return texts.T(l, failureKey(code))
}

func failureKey(code string) string { return "failure." + code }
