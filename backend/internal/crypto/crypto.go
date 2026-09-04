// Package crypto provides AES-256-GCM encryption at rest for secrets stored
// in the database (AI provider API keys). The key is derived from the
// AI_ENCRYPTION_KEY environment variable.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Box encrypts and decrypts byte slices with AES-256-GCM.
type Box struct {
	gcm cipher.AEAD
}

// NewBox derives the AES key from a passphrase.
func NewBox(passphrase string) (*Box, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase must not be empty")
	}
	key := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Box{gcm: gcm}, nil
}

// Encrypt returns a base64-encoded nonce||ciphertext.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := b.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt takes a base64-encoded nonce||ciphertext produced by Encrypt.
func (b *Box) Decrypt(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted value: %w", err)
	}
	nonceSize := b.gcm.NonceSize()
	if len(raw) < nonceSize {
		return nil, fmt.Errorf("encrypted value too short")
	}
	plaintext, err := b.gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// EncryptString / DecryptString are convenience wrappers.
func (b *Box) EncryptString(s string) (string, error) { return b.Encrypt([]byte(s)) }

func (b *Box) DecryptString(s string) (string, error) {
	out, err := b.Decrypt(s)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// MaskKey returns a display-safe representation of a secret (e.g. "••••abcd").
func MaskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}
