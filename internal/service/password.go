package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordRunes = 12
	MaxPasswordBytes = 1024
	passwordMemory   = 19 * 1024 // KiB: OWASP Argon2id minimum, t=2, p=1.
	passwordTime     = 2
	passwordThreads  = 1
	passwordKeyLen   = 32
)

var (
	ErrInvalidPassword = errors.New("password must contain at least 12 characters and at most 1024 bytes of valid UTF-8")
	ErrInvalidHash     = errors.New("invalid encoded password hash")
)

func HashPassword(password string) (string, error) {
	if !utf8.ValidString(password) || len(password) > MaxPasswordBytes || utf8.RuneCountInString(password) < MinPasswordRunes {
		return "", ErrInvalidPassword
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("generate password salt failed")
	}
	key := argon2.IDKey([]byte(password), salt, passwordTime, passwordMemory, passwordThreads, passwordKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		passwordMemory, passwordTime, passwordThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword bounds stored parameters before allocating memory. Never put
// password or encodedHash in an error message or a log field.
func VerifyPassword(password, encodedHash string) (bool, error) {
	if !utf8.ValidString(password) || len(password) > MaxPasswordBytes {
		return false, ErrInvalidPassword
	}
	if len(encodedHash) > 256 {
		return false, ErrInvalidHash
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrInvalidHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, ErrInvalidHash
	}
	var values [3]uint64
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], prefix) {
			return false, ErrInvalidHash
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(params[i], prefix), 10, 32)
		if err != nil {
			return false, ErrInvalidHash
		}
		values[i] = n
	}
	memory, iterations, parallelism := values[0], values[1], values[2]
	if parallelism < 1 || parallelism > 2 || memory < 8*parallelism || memory > 64*1024 || iterations < 1 || iterations > 5 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 32 {
		return false, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) != passwordKeyLen {
		return false, ErrInvalidHash
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(key)))
	return subtle.ConstantTimeCompare(actual, key) == 1, nil
}
