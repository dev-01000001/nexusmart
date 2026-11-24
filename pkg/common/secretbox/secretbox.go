package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Box encrypts and decrypts sensitive strings at rest using AES-256-GCM.
// Wire format: 12-byte random nonce || GCM ciphertext + 16-byte tag (AAD = nil).
// This format matches shipping's former TokenCipher so previously stored ciphertexts remain readable.
type Box struct {
	aead cipher.AEAD
}

func keyLabel(envName string) string {
	if s := strings.TrimSpace(envName); s != "" {
		return s
	}
	return "encryption key"
}

// NewFromBase64 builds a Box from a base64-encoded 32-byte AES-256 key.
// envName is used in validation error messages (e.g. "SHIPPING_CONFIG_KEY", "BANK_ACCOUNT_KEY");
// the key material itself is never included in errors.
func NewFromBase64(b64Key, envName string) (*Box, error) {
	label := keyLabel(envName)
	trimmed := strings.TrimSpace(b64Key)
	if trimmed == "" {
		return nil, fmt.Errorf("%s must not be empty", label)
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%s must be valid base64", label)
		}
	}
	return New(key, label)
}

// New builds a Box from raw 32-byte AES-256 key material.
func New(key []byte, envName string) (*Box, error) {
	label := keyLabel(envName)
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to 32 bytes (AES-256), got %d", label, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext with a fresh random nonce. Never includes plaintext or key in errors.
func (b *Box) Encrypt(plaintext string) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secretbox not initialized")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens ciphertext produced by Encrypt. Never includes key, plaintext, or ciphertext in errors.
func (b *Box) Decrypt(blob []byte) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("secretbox not initialized")
	}
	nonceSize := b.aead.NonceSize()
	if len(blob) < nonceSize+b.aead.Overhead() {
		return "", errors.New("encrypted payload is truncated")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("failed to decrypt payload")
	}
	return string(plain), nil
}
