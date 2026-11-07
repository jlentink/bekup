// Package riskaccept is the only reader and writer of risk_acceptances
// (D-228): admins (and backup owners for a job they own) accept a
// weakened condition (the instance posture, a connection's TLS mode or
// privileged role, the SMTP transport, a job's encryption opt-out) for one
// month, one year or forever. An acceptance covers exactly the condition
// it was made for (Condition.Hash, built only here from the database or
// the process configuration): a changed condition is flagged again.
//
// Flagging code asks Covered / AcceptedPosture; readers use Findings and
// List; the hourly security check runs Sweep (expiry warning, ends). The
// permanent badges and the audit entries stay: an accepted risk is only no
// longer an open issue.
package riskaccept
