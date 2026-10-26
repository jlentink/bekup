package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed" // the EFF wordlist
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"
)

// effLargeWordlist is the EFF large wordlist for dice-generated passphrases
// (7776 words), by the Electronic Frontier Foundation, licensed CC BY 3.0 US
// (https://www.eff.org/dice). Changed: the dice-roll column was removed,
// leaving one word per line. Attribution: NOTICE and
// docs/security/key-management.md.
//
//go:embed eff_large_wordlist.txt
var effLargeWordlist string

const (
	// WordlistSize is the number of words in the embedded EFF list.
	WordlistSize = 7776
	// MinPassphraseWords is the minimum (and default) passphrase length:
	// 10 words * log2(7776) = 129.2 bits of entropy.
	MinPassphraseWords = 10
	// MaxPassphraseWords bounds GeneratePassphrase.
	MaxPassphraseWords = 64
	// PassphraseSeparator joins the words. No word contains a space, so the
	// passphrase splits back into its words unambiguously and the entropy is
	// exactly words * log2(7776).
	PassphraseSeparator = " "
)

var (
	wordsOnce sync.Once
	words     []string
)

func wordlist() []string {
	wordsOnce.Do(func() {
		words = strings.Fields(effLargeWordlist)
	})
	return words
}

// PassphraseEntropyBits returns the entropy of a generated passphrase with
// n words.
func PassphraseEntropyBits(n int) float64 {
	return float64(n) * math.Log2(WordlistSize)
}

// GeneratePassphrase returns n words chosen uniformly and independently
// from the EFF large wordlist with crypto/rand, joined by
// PassphraseSeparator. n must be in [MinPassphraseWords, MaxPassphraseWords].
func GeneratePassphrase(n int) (*Secret, error) {
	if n < MinPassphraseWords || n > MaxPassphraseWords {
		return nil, fmt.Errorf("passphrase length must be %d..%d words", MinPassphraseWords, MaxPassphraseWords)
	}
	wl := wordlist()
	if len(wl) != WordlistSize {
		return nil, errors.New("embedded wordlist is corrupt")
	}
	upper := big.NewInt(int64(len(wl)))
	var buf bytes.Buffer
	buf.Grow(n * 10)
	for i := range n {
		idx, err := rand.Int(rand.Reader, upper)
		if err != nil {
			Zero(buf.Bytes())
			return nil, fmt.Errorf("reading randomness: %w", err)
		}
		if i > 0 {
			buf.WriteString(PassphraseSeparator)
		}
		buf.WriteString(wl[idx.Int64()])
	}
	return NewSecret(buf.Bytes()), nil
}

// AgeKeypair is a freshly generated age X25519 keypair.
type AgeKeypair struct {
	// Recipient is the public "age1..." string used to encrypt.
	Recipient string
	// Identity is the private "AGE-SECRET-KEY-1..." string. bekup shows it
	// once and never stores it (D-016). Destroy it after use.
	Identity *Secret
}

// GenerateAgeKeypair creates an age X25519 keypair from crypto/rand.
//
// The age library returns the identity as a Go string, which cannot be
// zeroed; the copy held in Identity can be.
func GenerateAgeKeypair() (*AgeKeypair, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generating age identity: %w", err)
	}
	return &AgeKeypair{Recipient: id.Recipient().String(), Identity: NewSecret([]byte(id.String()))}, nil
}

// SSHKeypair is a freshly generated ed25519 SSH keypair.
type SSHKeypair struct {
	// PrivateKeyPEM is the OpenSSH-format private key ("-----BEGIN OPENSSH
	// PRIVATE KEY-----"), unencrypted; bekup stores it sealed. Destroy it
	// after use.
	PrivateKeyPEM *Secret
	// AuthorizedKey is the single authorized_keys line (no trailing newline).
	AuthorizedKey string
	// Fingerprint is the OpenSSH "SHA256:..." fingerprint of the public key.
	Fingerprint string
}

// GenerateSSHKeypair creates an ed25519 SSH keypair from crypto/rand. The
// comment is appended to the authorized_keys line and embedded in the
// private key; it must not contain line breaks.
func GenerateSSHKeypair(comment string) (*SSHKeypair, error) {
	if strings.ContainsAny(comment, "\r\n") {
		return nil, errors.New("ssh key comment must be a single line")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}
	defer Zero(priv)
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, fmt.Errorf("encoding ssh private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(block)
	Zero(block.Bytes)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		Zero(pemBytes)
		return nil, fmt.Errorf("encoding ssh public key: %w", err)
	}
	line := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
	if comment != "" {
		line += " " + comment
	}
	return &SSHKeypair{
		PrivateKeyPEM: NewSecret(pemBytes),
		AuthorizedKey: line,
		Fingerprint:   ssh.FingerprintSHA256(sshPub),
	}, nil
}
