package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidKeyLength   = errors.New("aead: key must be exactly 32 bytes")
	ErrCiphertextTooShort = errors.New("aead: ciphertext is too short")
	ErrDecryptionFailed   = errors.New("aead: decryption failed or authentication tag mismatch")
)

const (
	AEADNonceSize    = 12
	AEADTagSize      = 16
	AEADHeaderSize   = 1 // 1 byte version prefix
	AEADMinCipherLen = AEADHeaderSize + AEADNonceSize + AEADTagSize // 29 bytes
)

// AEADCipher provides versioned AES-256-GCM AEAD encryption and decryption
type AEADCipher struct {
	key        [32]byte
	keyVersion uint16
}

// NewAEADCipher creates a new AEADCipher with a 32-byte key and version
func NewAEADCipher(key []byte, version uint16) (*AEADCipher, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeyLength
	}
	if version == 0 {
		version = 1
	}
	var k [32]byte
	copy(k[:], key)
	return &AEADCipher{
		key:        k,
		keyVersion: version,
	}, nil
}

// KeyVersion returns the configured key version
func (c *AEADCipher) KeyVersion() uint16 {
	return c.keyVersion
}

// Encrypt encrypts plaintext using AES-256-GCM with a random 12-byte nonce.
// Envelope format: [1-byte key version][12-byte nonce][ciphertext + 16-byte tag]
func (c *AEADCipher) Encrypt(plaintext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("aead: failed to create AES block cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aead: failed to create GCM cipher: %w", err)
	}

	nonce := make([]byte, AEADNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("aead: failed to generate random nonce: %w", err)
	}

	out := make([]byte, AEADHeaderSize+AEADNonceSize, AEADHeaderSize+AEADNonceSize+len(plaintext)+AEADTagSize)
	out[0] = byte(c.keyVersion)
	copy(out[AEADHeaderSize:], nonce)

	ciphertext := gcm.Seal(out, nonce, plaintext, aad)
	return ciphertext, nil
}

// Decrypt decrypts versioned AEAD ciphertext using AES-256-GCM.
func (c *AEADCipher) Decrypt(ciphertext []byte, aad []byte) ([]byte, error) {
	if len(ciphertext) < AEADMinCipherLen {
		// Attempt fallback for non-versioned legacy raw [nonce(12)][ct+tag(16)]
		if len(ciphertext) >= AEADNonceSize+AEADTagSize {
			block, err := aes.NewCipher(c.key[:])
			if err == nil {
				gcm, err := cipher.NewGCM(block)
				if err == nil {
					nonce := ciphertext[:AEADNonceSize]
					rawCT := ciphertext[AEADNonceSize:]
					if plain, err := gcm.Open(nil, nonce, rawCT, aad); err == nil {
						return plain, nil
					}
				}
			}
		}
		return nil, ErrCiphertextTooShort
	}

	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("aead: failed to create AES block cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aead: failed to create GCM cipher: %w", err)
	}

	// Try standard versioned header
	nonce := ciphertext[AEADHeaderSize : AEADHeaderSize+AEADNonceSize]
	rawCT := ciphertext[AEADHeaderSize+AEADNonceSize:]

	plaintext, err := gcm.Open(nil, nonce, rawCT, aad)
	if err != nil {
		// Fallback: try raw nonce without version header in case ciphertext was unversioned
		nonceRaw := ciphertext[:AEADNonceSize]
		rawCTRaw := ciphertext[AEADNonceSize:]
		if plain, errRaw := gcm.Open(nil, nonceRaw, rawCTRaw, aad); errRaw == nil {
			return plain, nil
		}
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}
