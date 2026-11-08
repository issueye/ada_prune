package secretbox

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	box, err := FromKey(make([]byte, KeySize))
	if err != nil {
		t.Fatalf("FromKey: %v", err)
	}
	const secret = "user_0123456789abcdef0123456789abcdef"
	sealed, err := box.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("ciphertext contains the plaintext")
	}
	plain, err := box.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plain != secret {
		t.Fatalf("round trip mismatch: %q", plain)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	box, _ := FromKey(make([]byte, KeySize))
	a, _ := box.Encrypt("same")
	b, _ := box.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	box, _ := FromKey(make([]byte, KeySize))
	sealed, _ := box.Encrypt("secret")
	// Tamper a middle byte of the payload: decode, flip, re-encode. Flipping
	// the last base64 character is not reliable — the decoder ignores the
	// final character's non-zero padding bits, and a one-in-four chance of
	// those bits carrying the whole change made this test flaky.
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode sealed: %v", err)
	}
	raw[len(raw)/2] ^= 0x40
	if _, err := box.Decrypt(base64.RawStdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
}

func TestFromKeyRejectsWrongLength(t *testing.T) {
	if _, err := FromKey(make([]byte, 16)); err == nil {
		t.Fatal("accepted a 16-byte key")
	}
}

func TestMask(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "…"},
		{"abcdef", "abcd…"},
		{"user_abcd1234wxyz", "user_abc…wxyz"},
		{"user_0123456789abcdef0123456789abcdef", "user_012…cdef"},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskNeverRevealsShortSecrets(t *testing.T) {
	const secret = "abc"
	if got := Mask(secret); strings.Contains(got, secret) {
		t.Fatalf("Mask(%q) = %q leaks the secret", secret, got)
	}
}

func TestOpenCreatesKeyWithRestrictedPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", ".secret.key")

	box, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if box == nil {
		t.Fatal("nil box")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	// POSIX permission bits are not meaningful on Windows, where the file
	// system reports the read-only attribute instead.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file mode = %o, want 600", perm)
		}
	}
	if size := info.Size(); size != KeySize {
		t.Fatalf("key file size = %d, want %d", size, KeySize)
	}

	// Reopening must reuse the same key, or previously encrypted secrets would
	// become unreadable.
	sealed, err := box.Encrypt("value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if plain, err := again.Decrypt(sealed); err != nil || plain != "value" {
		t.Fatalf("reopened box cannot decrypt: %v", err)
	}
}

func TestOpenRejectsCorruptKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".secret.key")
	if err := os.WriteFile(path, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("accepted a short key file")
	}
}
