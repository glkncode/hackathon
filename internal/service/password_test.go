package service

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	password := "correct-password-123"
	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(first, password) {
		t.Fatal("invalid hash format or reused salt")
	}
	if ok, err := VerifyPassword(password, first); err != nil || !ok {
		t.Fatalf("valid password rejected: %v", err)
	}
	if ok, err := VerifyPassword("wrong-password", first); err != nil || ok {
		t.Fatalf("wrong password accepted: %v", err)
	}
}

func TestPasswordBounds(t *testing.T) {
	for _, password := range []string{"", "short", strings.Repeat("x", MaxPasswordBytes+1), string([]byte{0xff})} {
		if _, err := HashPassword(password); !errors.Is(err, ErrInvalidPassword) {
			t.Fatal("invalid new password accepted")
		}
	}
	if _, err := HashPassword(strings.Repeat("я", 12)); err != nil {
		t.Fatal("minimum length must count characters")
	}
	if ok, err := VerifyPassword(strings.Repeat("x", MaxPasswordBytes+1), "broken"); ok || !errors.Is(err, ErrInvalidPassword) {
		t.Fatal("unbounded login password")
	}
}

func TestDamagedPasswordHashes(t *testing.T) {
	valid, err := HashPassword("correct-password-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{"", "not-a-hash", strings.Repeat("x", 257),
		strings.Replace(valid, "argon2id", "argon2i", 1), strings.Replace(valid, "v=19", "v=16", 1),
		strings.Replace(valid, "m=19456", "m=4294967295", 1), strings.Replace(valid, "t=2", "t=0", 1),
		strings.Replace(valid, "p=1", "p=0", 1), strings.Replace(valid, "p=1", "p=255", 1),
		strings.Replace(valid, "m=19456", "m=0", 1), strings.Replace(valid, "t=2", "t=500", 1),
		valid + "$extra", "$argon2id$v=19$m=19456,t=2,p=1$bad$bad",
	} {
		if ok, err := VerifyPassword("correct-password-123", encoded); ok || !errors.Is(err, ErrInvalidHash) {
			t.Errorf("damaged hash accepted")
		}
	}
}
