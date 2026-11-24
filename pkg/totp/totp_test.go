// SPDX-License-Identifier: AGPL-3.0-only

package totp

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The RFCs' own test vectors: both use the ASCII key "12345678901234567890".
func TestVectors(t *testing.T) {
	key := []byte("12345678901234567890")
	// RFC 4226, appendix D: six digits for counters 0 to 9.
	for counter, want := range []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"} {
		if got := HOTP(key, uint64(counter), 6); got != want {
			t.Errorf("HOTP counter %d: got %s want %s", counter, got, want)
		}
	}
	// RFC 6238, appendix B: SHA-1, eight digits, 30 second steps.
	for unix, want := range map[int64]string{
		59: "94287082", 1111111109: "07081804", 1111111111: "14050471", 1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130",
	} {
		if got := HOTP(key, uint64(unix/Period), 8); got != want {
			t.Errorf("TOTP at %d: got %s want %s", unix, got, want)
		}
	}
	// The six digit code an app shows is the tail of the eight digit one.
	if got, err := At(Base32.EncodeToString(key), time.Unix(59, 0)); err != nil || got != "287082" {
		t.Errorf("TOTP at 59 with six digits: got %s %v", got, err)
	}
	if _, err := At("not base32!", time.Unix(59, 0)); err == nil {
		t.Error("a malformed secret was accepted")
	}
}

func TestDrift(t *testing.T) {
	key := []byte("12345678901234567890")
	now := time.Unix(1_800_000_000, 0)
	current := now.Unix() / Period
	for offset, accepted := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code := HOTP(key, uint64(current+offset), Digits)
		step := Step(key, code, now)
		if accepted && step != current+offset || !accepted && step != 0 {
			t.Errorf("code from step %+d: got step %d (current %d)", offset, step, current)
		}
	}
	if Step(key, "", now) != 0 || Step(key, "12345", now) != 0 {
		t.Error("a malformed code was accepted")
	}
}

func TestURIAndCodes(t *testing.T) {
	uri := URI("Telrics Switch", "ada@example.test", []byte("12345678901234567890"))
	want := "otpauth://totp/Telrics%20Switch:ada%40example.test?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=Telrics%20Switch&algorithm=SHA1&digits=6&period=30"
	if uri != want {
		t.Errorf("uri: got %s", uri)
	}
	if PlainCode(" 123 456 ") != "123456" || PlainCode("ABCDE-fghij") != "abcdefghij" {
		t.Error("PlainCode")
	}
	if !IsDigits("012345", 6) || IsDigits("01234", 6) || IsDigits("01234a", 6) {
		t.Error("IsDigits")
	}
	plain, shown, err := NewRecoveryCode()
	if err != nil || len(plain) != 10 || PlainCode(shown) != plain || IsDigits(plain, 6) {
		t.Errorf("recovery code: %q %q %v", plain, shown, err)
	}
	secret, err := NewSecret()
	if err != nil || len(secret) != SecretBytes {
		t.Errorf("secret: %d bytes, %v", len(secret), err)
	}
}

func TestSealing(t *testing.T) {
	secret := []byte("12345678901234567890")
	// Without a key the secret is stored as it is, marked as such.
	var none *Sealer
	plain, err := none.Seal("user:7", secret)
	if err != nil || !bytes.Equal(plain[1:], secret) {
		t.Fatalf("plain: %v %v", plain, err)
	}
	if _, err := NewSealer("too short"); err == nil {
		t.Error("a short key was accepted")
	}
	seal, err := NewSealer(strings.Repeat("0f", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal.Seal("user:7", secret)
	if err != nil || bytes.Contains(sealed, secret) {
		t.Fatalf("sealed: %v %v", sealed, err)
	}
	if got, err := seal.Open("user:7", sealed); err != nil || !bytes.Equal(got, secret) {
		t.Errorf("open: %v %v", got, err)
	}
	// Rows written before the key was set stay readable.
	if got, err := seal.Open("user:7", plain); err != nil || !bytes.Equal(got, secret) {
		t.Errorf("open plain with a key set: %v %v", got, err)
	}
	// Another owner's row, a damaged row, another key or a missing key do not open.
	if _, err := seal.Open("user:8", sealed); err == nil {
		t.Error("a secret opened for another owner")
	}
	damaged := append([]byte{}, sealed...)
	damaged[len(damaged)-1] ^= 1
	if _, err := seal.Open("user:7", damaged); err == nil {
		t.Error("a damaged secret opened")
	}
	other, _ := NewSealer(strings.Repeat("a1", 32))
	if _, err := other.Open("user:7", sealed); err == nil {
		t.Error("a secret opened with another key")
	}
	if _, err := none.Open("user:7", sealed); err == nil {
		t.Error("a sealed secret opened without a key")
	}
	if _, err := seal.Open("user:7", []byte{9, 1, 2}); err == nil {
		t.Error("an unknown version opened")
	}
}
