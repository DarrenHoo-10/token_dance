package crypto

import (
	"testing"
)

func TestArgon2PasswordHashing(t *testing.T) {
	password := "SecretP@ssword123!"
	encoded, err := HashPassword(password, FastArgon2Params)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	match, err := VerifyPassword(password, encoded)
	if err != nil {
		t.Fatalf("failed to verify password: %v", err)
	}
	if !match {
		t.Errorf("expected password to match")
	}

	matchWrong, err := VerifyPassword("WrongPassword123!", encoded)
	if err != nil {
		t.Fatalf("unexpected error on wrong password: %v", err)
	}
	if matchWrong {
		t.Errorf("wrong password should not match")
	}
}

func TestCrockfordCode(t *testing.T) {
	code, err := GenerateCrockfordCode(8)
	if err != nil {
		t.Fatalf("failed to generate Crockford code: %v", err)
	}
	if len(code) != 8 {
		t.Errorf("expected length 8, got %d", len(code))
	}

	normalized := NormalizeCrockfordCode("7k-9m-i-l-o")
	// i -> 1, l -> 1, o -> 0
	if normalized != "7K9M110" {
		t.Errorf("expected 7K9M110, got %s", normalized)
	}
}

func TestSixDigitCode(t *testing.T) {
	code, err := GenerateSixDigitCode()
	if err != nil {
		t.Fatalf("failed to generate six digit code: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected length 6, got %d", len(code))
	}
}

func TestHMACSHA256(t *testing.T) {
	key := []byte("test-key")
	data := []byte("test-data")

	h1 := HMACSHA256(key, data)
	h2 := HMACSHA256(key, data)

	if h1 != h2 {
		t.Errorf("HMAC should be deterministic")
	}

	h3 := HMACSHA256(key, []byte("different-data"))
	if h1 == h3 {
		t.Errorf("different data should produce different HMAC")
	}
}
