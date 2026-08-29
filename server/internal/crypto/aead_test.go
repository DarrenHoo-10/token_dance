package crypto

import (
	"bytes"
	"testing"
)

func TestAEADCipher_RoundTrip(t *testing.T) {
	key := []byte("01234567890123456789012345678901") // 32 bytes
	cipher, err := NewAEADCipher(key, 1)
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}

	plaintext := []byte("user@tokendance.dev")
	aad := []byte("users.email")

	ciphertext, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	// Verify ciphertext does not contain plaintext
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatalf("ciphertext must not contain plaintext email")
	}

	decrypted, err := cipher.Decrypt(ciphertext, aad)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("expected decrypted %s, got %s", string(plaintext), string(decrypted))
	}
}

func TestAEADCipher_InvalidKeyLength(t *testing.T) {
	_, err := NewAEADCipher([]byte("too-short"), 1)
	if err != ErrInvalidKeyLength {
		t.Fatalf("expected ErrInvalidKeyLength, got %v", err)
	}
}

func TestAEADCipher_TamperAndAADMismatch(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	cipher, err := NewAEADCipher(key, 1)
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}

	plaintext := []byte("sensitive-payload")
	aad := []byte("email_outbox.payload")

	ciphertext, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	// 1. Wrong AAD
	_, err = cipher.Decrypt(ciphertext, []byte("wrong.aad"))
	if err == nil {
		t.Fatalf("decryption should fail with wrong AAD")
	}

	// 2. Tampered ciphertext
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = cipher.Decrypt(tampered, aad)
	if err == nil {
		t.Fatalf("decryption should fail with tampered ciphertext")
	}

	// 3. Different key
	otherKey := []byte("11234567890123456789012345678901")
	otherCipher, _ := NewAEADCipher(otherKey, 1)
	_, err = otherCipher.Decrypt(ciphertext, aad)
	if err == nil {
		t.Fatalf("decryption should fail with different key")
	}
}
