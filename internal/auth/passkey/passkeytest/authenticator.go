// Package passkeytest provides a software WebAuthn authenticator for tests:
// an ECDSA P-256 key that answers registration options with a valid
// attestation "none" response and request options with a valid assertion,
// as a browser plus authenticator would. Origin, RP ID and the signature
// counter are settable so tests can provoke wrong-origin, wrong-RP-ID and
// cloned-authenticator cases. Never use it outside tests.
package passkeytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Authenticator flags (WebAuthn §6.1).
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// Authenticator is a virtual authenticator with one credential.
type Authenticator struct {
	// Origin is put into clientDataJSON (what the browser would report).
	Origin string
	// RPID is hashed into the authenticator data. Empty: the RP ID from
	// the options.
	RPID string
	// SignCount is the current signature counter; CounterStep is added
	// before each assertion (0: an authenticator without a counter).
	SignCount   uint32
	CounterStep uint32
	// UserVerified sets the UV flag; BackupEligible/BackedUp the BE/BS
	// flags.
	UserVerified   bool
	BackupEligible bool
	BackedUp       bool
	// AAGUID is reported at registration.
	AAGUID [16]byte
	// Transports are reported at registration.
	Transports []string

	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
}

// New returns an authenticator for origin with a fresh key and a random
// 32-byte credential id, a counter that starts at 0 and steps by 1, and
// user verification.
func New(origin string) (*Authenticator, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	a := &Authenticator{Origin: origin, CounterStep: 1, UserVerified: true, key: key, credentialID: id, Transports: []string{"usb"}}
	copy(a.AAGUID[:], "bekup-test-authn")
	return a, nil
}

// Clone returns a copy with the same key, credential and counter (a
// cloned authenticator).
func (a *Authenticator) Clone() *Authenticator {
	c := *a
	c.credentialID = append([]byte(nil), a.credentialID...)
	c.userHandle = append([]byte(nil), a.userHandle...)
	c.Transports = append([]string(nil), a.Transports...)
	return &c
}

// CredentialID returns the credential id.
func (a *Authenticator) CredentialID() []byte { return append([]byte(nil), a.credentialID...) }

var b64 = base64.RawURLEncoding

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
	} `json:"publicKey"`
}

func (a *Authenticator) rpID(fromOptions string) string {
	if a.RPID != "" {
		return a.RPID
	}
	return fromOptions
}

func (a *Authenticator) flags() byte {
	f := byte(flagUP)
	if a.UserVerified {
		f |= flagUV
	}
	if a.BackupEligible {
		f |= flagBE
	}
	if a.BackedUp {
		f |= flagBS
	}
	return f
}

func clientData(typ, challenge, origin string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	if err != nil {
		panic(err) // strings and a bool always encode
	}
	return b
}

// Register answers creation options (the JSON returned by
// BeginRegistration) with an attestation response JSON.
func (a *Authenticator) Register(options []byte) ([]byte, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, fmt.Errorf("passkeytest: creation options: %w", err)
	}
	if o.PublicKey.Challenge == "" {
		return nil, errors.New("passkeytest: creation options without challenge")
	}
	uh, err := b64.DecodeString(o.PublicKey.User.ID)
	if err != nil {
		return nil, fmt.Errorf("passkeytest: user id: %w", err)
	}
	a.userHandle = uh
	point, err := a.key.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil || len(point) != 65 {
		return nil, fmt.Errorf("passkeytest: public key: %w", err)
	}
	x, y := point[1:33], point[33:]
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         1, XCoord: x, YCoord: y,
	})
	if err != nil {
		return nil, err
	}
	rpHash := sha256.Sum256([]byte(a.rpID(o.PublicKey.RP.ID)))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, a.flags()|flagAT)
	auth = binary.BigEndian.AppendUint32(auth, a.SignCount)
	auth = append(auth, a.AAGUID[:]...)
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(a.credentialID))) //nolint:gosec // G115: 32 bytes
	auth = append(auth, a.credentialID...)
	auth = append(auth, cose...)
	attObj, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credentialID), "rawId": b64.EncodeToString(a.credentialID),
		"type": "public-key", "authenticatorAttachment": "cross-platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", o.PublicKey.Challenge, a.Origin)),
			"attestationObject": b64.EncodeToString(attObj),
			"transports":        a.Transports,
		},
	})
}

// Assert answers request options (the JSON returned by BeginAssertion)
// with an assertion response JSON, advancing the counter by CounterStep.
func (a *Authenticator) Assert(options []byte) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, fmt.Errorf("passkeytest: request options: %w", err)
	}
	if o.PublicKey.Challenge == "" {
		return nil, errors.New("passkeytest: request options without challenge")
	}
	a.SignCount += a.CounterStep
	rpHash := sha256.Sum256([]byte(a.rpID(o.PublicKey.RPID)))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, a.flags())
	auth = binary.BigEndian.AppendUint32(auth, a.SignCount)
	cd := clientData("webauthn.get", o.PublicKey.Challenge, a.Origin)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credentialID), "rawId": b64.EncodeToString(a.credentialID),
		"type": "public-key", "authenticatorAttachment": "cross-platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
	})
}
