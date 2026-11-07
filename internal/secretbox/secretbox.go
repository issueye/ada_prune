// Package secretbox encrypts upstream credentials at rest and masks them on the
// way out.
//
// The key lives in data/.secret.key as 32 raw bytes, created on first start with
// 0600 permissions. Losing the key means losing the ciphertext: it is not
// derivable from anything else on disk, by design.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeySize is the AES-256 key length.
const KeySize = 32

// ErrNoKey means the key file is missing or malformed.
var ErrNoKey = errors.New("secretbox: invalid key file")

// Box encrypts and decrypts short secrets with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// Open loads the key at path, creating it when absent.
func Open(path string) (*Box, error) {
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	return FromKey(key)
}

// FromKey builds a Box from raw key material. It is the test-friendly entry
// point.
func FromKey(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// GenerateKey returns fresh random key material.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secretbox: generate key: %w", err)
	}
	return key, nil
}

// Encrypt returns a base64 string of nonce||ciphertext.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if b == nil {
		return "", errors.New("secretbox: nil box")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(encoded string) (string, error) {
	if b == nil {
		return "", errors.New("secretbox: nil box")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("secretbox: decode: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secretbox: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("secretbox: open: %w", err)
	}
	return string(plain), nil
}

// Mask keeps the first 8 and last 4 characters of a secret, like
// user_abcd1234…wxyz. Short secrets are hidden entirely: a mask that still shows
// most of a five-character key is not a mask.
func Mask(secret string) string {
	if secret == "" {
		return ""
	}
	runes := []rune(secret)
	switch {
	case len(runes) <= 4:
		return "…"
	case len(runes) <= 14:
		return string(runes[:4]) + "…"
	default:
		return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
	}
}

// IsMask reports whether a value looks like the output of Mask. Update
// endpoints use it to mean "keep the stored secret".
func IsMask(v string) bool {
	return strings.HasSuffix(v, "…") || strings.Contains(v, "…")
}

func loadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != KeySize {
			return nil, fmt.Errorf("%w: %s holds %d bytes, want %d", ErrNoKey, path, len(raw), KeySize)
		}
		return raw, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretbox: read key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("secretbox: create directory: %w", err)
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	// O_EXCL makes two concurrent starts fail loudly instead of overwriting a
	// key that already encrypted data.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("secretbox: create key: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(key); err != nil {
		return nil, fmt.Errorf("secretbox: write key: %w", err)
	}
	return key, nil
}
