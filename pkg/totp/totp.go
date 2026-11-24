// SPDX-License-Identifier: AGPL-3.0-only

// Package totp implements time-based one-time passwords (RFC 6238 over
// RFC 4226, HMAC-SHA-1, 30 second steps, six digits: what authenticator
// apps expect) and the sealing of their secrets at rest.
package totp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Period      = 30 // seconds a code is shown for
	Digits      = 6
	SecretBytes = 20
)

// Base32 is how a secret is written for people and apps: no padding.
var Base32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// HOTP is the RFC 4226 code for a counter value.
func HOTP(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	at := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[at:at+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}

// At is the code an authenticator shows for a base32 secret at a moment.
func At(secret string, at time.Time) (string, error) {
	key, err := Base32.DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", err
	}
	return HOTP(key, uint64(at.Unix()/Period), Digits), nil
}

// Step finds the time step a code belongs to: the current one, or the one
// before or after it for clocks that drift. Zero means no match. Whoever
// accepts the code must remember the step and refuse it and earlier ones
// from then on, so that a code works once.
func Step(key []byte, code string, now time.Time) int64 {
	current := now.Unix() / Period
	var found int64
	for _, step := range []int64{current - 1, current, current + 1} {
		if step > 0 && subtle.ConstantTimeCompare([]byte(HOTP(key, uint64(step), Digits)), []byte(code)) == 1 {
			found = step
		}
	}
	return found
}

// NewSecret returns a fresh random secret.
func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretBytes)
	_, err := rand.Read(secret)
	return secret, err
}

// URI is the otpauth:// address an authenticator app reads, usually from a
// QR code, to take the secret in.
func URI(issuer, account string, secret []byte) string {
	// Authenticator apps want %20, not +, for a space.
	esc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		esc(issuer), esc(account), Base32.EncodeToString(secret), esc(issuer), Digits, Period)
}

// PlainCode drops the spaces and dashes people type or paste with a code.
func PlainCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}

// IsDigits reports whether s is exactly n decimal digits.
func IsDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// NewRecoveryCode returns a single-use code for a lost device (50 random
// bits) as it is stored, and as it is shown: abcde-fghij. PlainCode turns
// what a person types back into the stored form.
func NewRecoveryCode() (plain, shown string, err error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	plain = strings.ToLower(Base32.EncodeToString(raw))[:10]
	return plain, plain[:5] + "-" + plain[5:], nil
}

// ---- the secret at rest ----

// Whoever checks codes has to compute them, so the secret cannot be hashed.
// It is sealed with AES-256-GCM when the program was given a key, and
// stored as it is when not; a version byte says which, so both kinds can be
// read back.
const (
	sealedPlain = 0
	sealedAES   = 1
)

// Sealer seals secrets for storage. A nil Sealer stores them unencrypted.
type Sealer struct{ aead cipher.AEAD }

// NewSealer takes the key: 32 bytes as 64 hexadecimal digits. Secrets
// sealed with a key cannot be read without it.
func NewSealer(hexKey string) (*Sealer, error) {
	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(key) != 32 {
		return nil, errors.New("the secret key must be 32 bytes as 64 hexadecimal digits")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead}, nil
}

// Seal returns what to store for a secret. bound names the owner (a kind
// and an id, say) and is tied to the sealed text, so that one owner's row
// cannot be copied onto another's.
func (s *Sealer) Seal(bound string, secret []byte) ([]byte, error) {
	if s == nil {
		return append([]byte{sealedPlain}, secret...), nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(append([]byte{sealedAES}, nonce...), nonce, secret, []byte(bound)), nil
}

// Open reads back what Seal returned for the same owner.
func (s *Sealer) Open(bound string, stored []byte) ([]byte, error) {
	switch {
	case len(stored) > 1 && stored[0] == sealedPlain:
		return stored[1:], nil
	case len(stored) > 1 && stored[0] == sealedAES:
		if s == nil {
			return nil, errors.New("a two-step verification secret is encrypted, but no secret key is set")
		}
		n := 1 + s.aead.NonceSize()
		if len(stored) < n {
			break
		}
		return s.aead.Open(nil, stored[1:n], stored[n:], []byte(bound))
	}
	return nil, errors.New("unreadable two-step verification secret")
}
