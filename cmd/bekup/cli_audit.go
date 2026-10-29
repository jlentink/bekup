package main

import (
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

// newCLIAuditor is the auditor of the break-glass and maintenance commands
// (bekup admin, certs, audit sign-head, reindex --import). Its observer is
// serve's (notify.ObserveAudit): every CLI entry also writes its
// security.system alert (D-069) to the notification outbox in the same
// transaction, and a running serve's dispatcher delivers it to every admin
// (inbox + e-mail; task 5.5, D-190). A command whose alert cannot be
// written fails like one whose audit entry cannot be written.
func newCLIAuditor(db sqlc.DBTX) *audit.Auditor {
	return audit.New(db, audit.Options{Observer: notify.ObserveAudit})
}
