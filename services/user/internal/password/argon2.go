package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// Default OWASP-recommended Argon2id parameters (19 MiB, 2 iterations, 1 thread).
	defaultMemory      uint32 = 19 * 1024
	defaultIterations  uint32 = 2
	defaultParallelism uint8  = 1
	defaultSaltLength  uint32 = 16
	defaultKeyLength   uint32 = 32
)

var (
	ErrInvalidHash         = errors.New("encoded password hash has invalid format")
	ErrIncompatibleVersion = errors.New("incompatible argon2 version")
	ErrWeakPassword        = errors.New("password must be at least 8 characters")
)

// Hasher hashes and verifies passwords with Argon2id.
type Hasher struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

// NewDefaultHasher creates an Argon2id Hasher with secure OWASP parameters.
func NewDefaultHasher() *Hasher {
	return &Hasher{
		memory:      defaultMemory,
		iterations:  defaultIterations,
		parallelism: defaultParallelism,
		saltLength:  defaultSaltLength,
		keyLength:   defaultKeyLength,
	}
}

// ValidateStrength checks minimum password requirements.
func ValidateStrength(plain string) error {
	if len(plain) < 8 {
		return ErrWeakPassword
	}
	return nil
}

// Hash produces a PHC-formatted Argon2id password hash.
func (h *Hasher) Hash(plain string) (string, error) {
	salt := make([]byte, h.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(plain), salt, h.iterations, h.memory, h.parallelism, h.keyLength)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memory, h.iterations, h.parallelism, b64Salt, b64Hash), nil
}

// Verify checks whether plain matches the PHC-formatted Argon2id hash in constant time.
func (h *Hasher) Verify(plain, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrIncompatibleVersion
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	computed := argon2.IDKey([]byte(plain), salt, iterations, memory, parallelism, uint32(len(expectedHash)))
	if subtle.ConstantTimeCompare(expectedHash, computed) == 1 {
		return true, nil
	}
	return false, nil
}
