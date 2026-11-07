package riskaccept

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Kind is what is accepted (the risk_kind enum).
type Kind string

// The acceptable kinds (D-228). The four posture kinds share their names
// with the notify posture alert kinds.
const (
	KindInsecureTransport Kind = "insecure_transport"
	KindInsecureDBTLS     Kind = "insecure_db_tls"
	KindInsecureDBRoles   Kind = "insecure_db_roles"
	KindUnpinnedImage     Kind = "unpinned_image"
	KindConnectionTLS     Kind = "connection_tls"
	KindPrivilegedRole    Kind = "privileged_role"
	KindSMTPUnencrypted   Kind = "smtp_unencrypted"
	KindEncryptionOptOut  Kind = "encryption_opt_out"
)

// Kinds lists every kind (the order of the findings: instance first).
var Kinds = []Kind{
	KindInsecureTransport, KindInsecureDBTLS, KindInsecureDBRoles, KindUnpinnedImage,
	KindSMTPUnencrypted, KindConnectionTLS, KindPrivilegedRole, KindEncryptionOptOut,
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// Instance reports whether the kind concerns the whole instance (no
// subject row; admins only).
func (k Kind) Instance() bool {
	switch k {
	case KindInsecureTransport, KindInsecureDBTLS, KindInsecureDBRoles, KindUnpinnedImage, KindSMTPUnencrypted:
		return true
	default:
		return false
	}
}

// OnConnection reports whether the subject is a connection.
func (k Kind) OnConnection() bool { return k == KindConnectionTLS || k == KindPrivilegedRole }

// OnJob reports whether the subject is a job (the only kind a backup
// owner may accept).
func (k Kind) OnJob() bool { return k == KindEncryptionOptOut }

// Condition is a weakened condition as bekup sees it now. It is built
// only by the functions below (never from client input); Hash identifies
// it, so a changed condition is a different risk.
type Condition struct {
	Kind         Kind
	ConnectionID uuid.UUID
	JobID        uuid.UUID
	Hash         [32]byte
	// Display is a short readable form ("TLS disabled (db.internal:5432)").
	Display string
}

// ID is the opaque condition id the API shows and an acceptance must
// name (hex of the hash).
func (c Condition) ID() string { return hex.EncodeToString(c.Hash[:]) }

// key identifies a subject: one live acceptance per kind and subject.
type key struct {
	kind Kind
	conn uuid.UUID
	job  uuid.UUID
}

func (c Condition) key() key { return key{c.Kind, c.ConnectionID, c.JobID} }

// maxDisplay bounds Condition.Display (risk_acceptances_condition_length).
const maxDisplay = 300

// build hashes the canonical encoding: a version prefix, the kind, the
// subject and the fields sorted by name, each length-prefixed (no two
// different conditions share an encoding).
func build(kind Kind, conn, job uuid.UUID, display string, fields map[string]string) Condition {
	h := sha256.New()
	put := func(s string) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s))) //nolint:gosec // G115: field lengths are tiny
		h.Write(n[:])
		h.Write([]byte(s))
	}
	put("bekup-risk-v1")
	put(string(kind))
	put(conn.String())
	put(job.String())
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		put(k)
		put(fields[k])
	}
	c := Condition{Kind: kind, ConnectionID: conn, JobID: job, Display: clip(display)}
	copy(c.Hash[:], h.Sum(nil))
	return c
}

// clip bounds a display text to maxDisplay runes without control
// characters.
func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) <= maxDisplay {
		return s
	}
	r := []rune(s)
	return string(r[:maxDisplay-1]) + "…"
}

// RouteDirect is the route of a connection without an SSH tunnel.
const RouteDirect = "direct"

// Route is the canonical route of a connection: direct, or the SSH
// bastion (user, host, port). A different route is a different risk: the
// credentials and the dump travel another way (D-228 review).
func Route(ssh bool, user, host string, port int) string {
	if !ssh {
		return RouteDirect
	}
	return "ssh:" + user + "@" + strings.ToLower(endpoint(host, port))
}

func endpoint(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

// InsecureTransport is the condition: serve's base URL is not https (D-095). The URL is
// part of the condition.
func InsecureTransport(baseURL string, allowInsecureHTTP bool) Condition {
	return build(KindInsecureTransport, uuid.Nil, uuid.Nil, "bekup is served over plain HTTP ("+baseURL+")",
		map[string]string{"base_url": baseURL, "allow_insecure_http": strconv.FormatBool(allowInsecureHTTP)})
}

// InsecureDBTLS is the condition: the app database's tls_mode is require or disable
// (D-100); a different mode or CA file choice is a different risk.
func InsecureDBTLS(mode string, caFileSet bool, host string) Condition {
	d := "App database TLS mode " + mode
	if mode == "require" && caFileSet {
		d += " (CA checked, host name not verified)"
	}
	return build(KindInsecureDBTLS, uuid.Nil, uuid.Nil, d,
		map[string]string{"tls_mode": mode, "ca_file_set": strconv.FormatBool(caFileSet), "host": strings.ToLower(host)})
}

// InsecureDBRoles is the condition: single-account database mode (D-207).
func InsecureDBRoles() Condition {
	return build(KindInsecureDBRoles, uuid.Nil, uuid.Nil, "One database account for migrations and the app (single-account mode)",
		map[string]string{"single_account": "true"})
}

// UnpinnedImage is the condition: the bekup image follows a tag instead of a digest (D-212).
func UnpinnedImage() Condition {
	return build(KindUnpinnedImage, uuid.Nil, uuid.Nil, "The bekup image follows a tag instead of a pinned digest",
		map[string]string{"image_mode": "following"})
}

// SMTPUnencrypted is the condition: the SMTP mode may send without TLS (auto or none,
// D-180); only when sending is configured and enabled. ok false: no risk.
func SMTPUnencrypted(mode, host string, port int, username string) (Condition, bool) {
	if mode != "auto" && mode != "none" {
		return Condition{}, false
	}
	d := "SMTP mode " + mode + " (" + endpoint(host, port) + ") may send e-mail unencrypted"
	return build(KindSMTPUnencrypted, uuid.Nil, uuid.Nil, d,
		map[string]string{"mode": mode, "host": strings.ToLower(host), "port": strconv.Itoa(port), "username": username}), true
}

// ConnectionTLS is the condition: a connection's TLS mode other than pinned (lower-case
// database value). ok false: pinned, no risk.
func ConnectionTLS(id uuid.UUID, mode, host string, port int, route string) (Condition, bool) {
	var d string
	switch mode {
	case "unverified":
		d = "TLS without verification"
	case "disabled":
		d = "TLS disabled"
	default:
		return Condition{}, false
	}
	via := ""
	if route != RouteDirect {
		via = " via SSH"
	}
	return build(KindConnectionTLS, id, uuid.Nil, d+" ("+endpoint(host, port)+via+")",
		map[string]string{"tls_mode": mode, "host": strings.ToLower(host), "port": strconv.Itoa(port), "route": route}), true
}

// PrivilegedRole is the condition: a connection's stored privilege verdict is privileged
// or unknown (D-068; unknown counts as privileged). The verdict, its
// reasons and the role are part of the condition. ok false: no risk.
func PrivilegedRole(id uuid.UUID, status string, reasons []string, host string, port int, username, route string) (Condition, bool) {
	var d string
	switch status {
	case "privileged":
		d = "Privileged role " + username
	case "unknown":
		d = "Role " + username + " with unknown privileges (treated as privileged)"
	default:
		return Condition{}, false
	}
	rs := slices.Clone(reasons)
	slices.Sort(rs)
	rs = slices.Compact(rs)
	if len(rs) > 0 {
		d += ": " + strings.Join(rs, ", ")
	}
	return build(KindPrivilegedRole, id, uuid.Nil, d,
		map[string]string{
			"status": status, "reasons": strings.Join(rs, "\n"), "host": strings.ToLower(host),
			"port": strconv.Itoa(port), "username": username, "route": route,
		}), true
}

// EncryptionOptOut is the condition: a job's backups are not encrypted (D-142). The opt-out
// time is the condition: a later opt-out is a new decision. ok false: the
// job is encrypted.
func EncryptionOptOut(job uuid.UUID, optedOutAt *time.Time) (Condition, bool) {
	if optedOutAt == nil {
		return Condition{}, false
	}
	return build(KindEncryptionOptOut, uuid.Nil, job, "Backups are not encrypted (opted out "+optedOutAt.UTC().Format("2006-01-02")+")",
		map[string]string{"opted_out_at": strconv.FormatInt(optedOutAt.UTC().UnixMicro(), 10)}), true
}
