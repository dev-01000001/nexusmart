package service

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

// TokenCipher encrypts and decrypts carrier API tokens using AES-256-GCM (UC-A12).
// Ciphertext format: 12-byte random nonce || GCM ciphertext + tag.
type TokenCipher struct {
	aead cipher.AEAD
}

// NewTokenCipherFromBase64 builds a TokenCipher from a base64-encoded 32-byte AES-256 key.
func NewTokenCipherFromBase64(b64Key string) (*TokenCipher, error) {
	trimmed := strings.TrimSpace(b64Key)
	if trimmed == "" {
		return nil, errors.New("SHIPPING_CONFIG_KEY must not be empty")
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, errors.New("SHIPPING_CONFIG_KEY must be valid base64")
		}
	}
	return NewTokenCipher(key)
}

// NewTokenCipher builds a TokenCipher from raw 32-byte key material.
func NewTokenCipher(key []byte) (*TokenCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("SHIPPING_CONFIG_KEY must decode to 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &TokenCipher{aead: aead}, nil
}

// Encrypt seals plaintext with a fresh random nonce. Never includes plaintext in errors.
func (c *TokenCipher) Encrypt(plaintext string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("token cipher not initialized")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens ciphertext produced by Encrypt. Never includes key or plaintext in errors.
func (c *TokenCipher) Decrypt(blob []byte) (string, error) {
	if c == nil || c.aead == nil {
		return "", errors.New("token cipher not initialized")
	}
	nonceSize := c.aead.NonceSize()
	if len(blob) < nonceSize {
		return "", errors.New("encrypted token payload is truncated")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("failed to decrypt carrier token")
	}
	return string(plain), nil
}
