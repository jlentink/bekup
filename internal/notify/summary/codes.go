package summary

// failureTexts are the short, fixed texts of run error codes
// (runs.error_code: the jobs.Code* codes and the runner's normalized
// reasons, lower-cased) shown next to a failed job in the summary. They are
// bekup's own texts, never server output; an unknown code shows only
// "failed". TestFailureTextsCoverCodes keeps the list complete.
var failureTexts = map[string]string{
	// Runner reasons (runnerpb.Reason, lower-cased by the backup worker).
	"target_unreachable":        "target unreachable",
	"target_auth_failed":        "authentication failed",
	"target_tls_failed":         "TLS connection failed",
	"tls_pin_mismatch":          "TLS certificate pin mismatch",
	"target_timeout":            "target timed out",
	"target_permission_denied":  "permission denied",
	"target_unsupported_server": "unsupported server version",
	"target_database_not_found": "database not found",
	"target_database_exists":    "database already exists",
	"ssh_host_key_mismatch":     "SSH host key mismatch",
	"ssh_auth_failed":           "SSH authentication failed",
	"ssh_host_key_unsupported":  "SSH host key type not supported",
	"target_failed":             "database operation failed",
	// Backup worker codes (jobs.Code*).
	"job_missing":              "job missing",
	"job_deleted":              "job deleted",
	"job_disabled":             "job disabled",
	"connection_deleted":       "connection deleted",
	"encryption_unavailable":   "encryption keys unavailable",
	"destinations_unavailable": "destinations unavailable",
	"destination_dropped":      "a destination was dropped",
	"all_destinations_failed":  "all destinations failed",
	"no_databases":             "no databases to back up",
	"database_missing":         "database missing",
	"dump_failed":              "dump failed",
	"dump_incomplete":          "dump incomplete",
	"verify_failed":            "verification failed",
	"manifest_failed":          "manifest could not be written",
	"incomplete":               "incomplete",
	"timeout":                  "timed out",
	"canceled":                 "canceled",
	"interrupted":              "interrupted (worker stopped)",
	"runner_unavailable":       "runner unavailable",
	"runner_busy":              "runner busy",
	"runner_rejected":          "runner rejected the request",
	"target_not_allowed":       "target not allowed by the runner",
	"runner_failed":            "runner failed",
	"storage_failed":           "storage failed",
	"internal":                 "internal error",
	"roles_without_passwords":  "roles without passwords",
	"slot_lost":                "run slot lost",
	"security_audit_failed":    "security audit failed",
}

// failureText is the fixed text of a run error code ("" when unknown).
func failureText(code string) string { return failureTexts[code] }
