package webhook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Header names of a delivery.
const (
	HeaderEvent     = "X-Bekup-Event"
	HeaderDelivery  = "X-Bekup-Delivery"
	HeaderTimestamp = "X-Bekup-Timestamp"
	HeaderSignature = "X-Bekup-Signature"
)

// SignatureVersion is the scheme prefix in X-Bekup-Signature.
const SignatureVersion = "v1"

// DefaultReplayWindow is the timestamp tolerance receivers should apply.
const DefaultReplayWindow = 5 * time.Minute

// Signing keys.
const (
	// KeyPrefix starts every generated signing key.
	KeyPrefix = "bkwh_"
	// keyRandomBytes is the entropy of a generated key.
	keyRandomBytes = 32
	// MinKeyLength is the shortest signing key Send accepts.
	MinKeyLength = 32
	// MaxKeyLength is the longest signing key Send accepts.
	MaxKeyLength = 256
)

// Signature errors.
var (
	ErrKeyInvalid         = errors.New("webhook: signing key must be 32 to 256 bytes")
	ErrSignatureMissing   = errors.New("webhook: signature or timestamp missing")
	ErrSignatureMalformed = errors.New("webhook: signature or timestamp malformed")
	ErrSignatureExpired   = errors.New("webhook: timestamp outside the replay window")
	ErrSignatureMismatch  = errors.New("webhook: signature mismatch")
)

// GenerateHMACKey returns a new signing key: "bkwh_" followed by 32 bytes
// from crypto/rand in unpadded base64url (48 bytes in total). The whole
// string, as UTF-8 bytes, is the HMAC key; receivers use it verbatim, so
// the returned bytes are also the display form, shown to the user exactly
// once. Store it sealed (crypto.Keyring.Seal) and zero it after use.
func GenerateHMACKey() ([]byte, error) {
	raw := make([]byte, keyRandomBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("webhook: generate signing key: %w", err)
	}
	out := make([]byte, len(KeyPrefix)+base64.RawURLEncoding.EncodedLen(len(raw)))
	copy(out, KeyPrefix)
	base64.RawURLEncoding.Encode(out[len(KeyPrefix):], raw)
	return out, nil
}

// DisplayHMACKey returns the form of a signing key shown to its owner once
// after generation or rotation. It is the key itself.
func DisplayHMACKey(key []byte) string { return string(key) }

func checkKey(key []byte) error {
	if len(key) < MinKeyLength || len(key) > MaxKeyLength {
		return ErrKeyInvalid
	}
	return nil
}

// Sign returns the X-Bekup-Signature value of one delivery sent at
// timestamp ts (unix seconds): "v1=" + lowercase hex
// HMAC-SHA256(key, "<ts>\n<delivery id>\n<event type>\n<body>") (D-198).
// The delivery id and event type are the X-Bekup-Delivery and
// X-Bekup-Event headers; signing them stops a replay under another
// delivery id (dedup bypass) or event type. None of the three header
// values can contain a newline, so the signed string is unambiguous.
func Sign(key []byte, ts int64, deliveryID, eventType string, body []byte) string {
	return SignatureVersion + "=" + hex.EncodeToString(mac(key, ts, deliveryID, eventType, body))
}

func mac(key []byte, ts int64, deliveryID, eventType string, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(strconv.AppendInt(nil, ts, 10))
	m.Write([]byte{'\n'})
	m.Write([]byte(deliveryID))
	m.Write([]byte{'\n'})
	m.Write([]byte(eventType))
	m.Write([]byte{'\n'})
	m.Write(body)
	return m.Sum(nil)
}

// Verify is the receiver-side check (also used by the tests): timestamp
// within window of now (DefaultReplayWindow if window is 0), then at least
// one "v1=<hex>" entry of the comma-separated signature header matching in
// constant time over the timestamp, delivery id, event type and body.
// Receivers should additionally drop repeated X-Bekup-Delivery values seen
// within the window.
func Verify(key []byte, signatureHeader, timestampHeader, deliveryHeader, eventHeader string, body []byte, now time.Time, window time.Duration) error {
	if signatureHeader == "" || timestampHeader == "" || deliveryHeader == "" || eventHeader == "" {
		return ErrSignatureMissing
	}
	if strings.ContainsAny(deliveryHeader, "\r\n") || strings.ContainsAny(eventHeader, "\r\n") {
		return ErrSignatureMalformed
	}
	if window <= 0 {
		window = DefaultReplayWindow
	}
	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil || ts <= 0 || len(timestampHeader) > 12 {
		return ErrSignatureMalformed
	}
	age := now.Sub(time.Unix(ts, 0))
	if age > window || age < -window {
		return ErrSignatureExpired
	}
	want := mac(key, ts, deliveryHeader, eventHeader, body)
	found := false
	for part := range strings.SplitSeq(signatureHeader, ",") {
		v, ok := strings.CutPrefix(strings.TrimSpace(part), SignatureVersion+"=")
		if !ok {
			continue
		}
		got, err := hex.DecodeString(v)
		if err != nil {
			continue
		}
		found = true
		if hmac.Equal(got, want) {
			return nil
		}
	}
	if !found {
		return ErrSignatureMalformed
	}
	return ErrSignatureMismatch
}
