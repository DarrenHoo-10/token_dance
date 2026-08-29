package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidKeyLength   = errors.New("aead: key must be exactly 32 bytes")
	ErrCiphertextTooShort = errors.New("aead: ciphertext is too short")
	ErrUnknownKeyVersion  = errors.New("aead: unknown key version")
	ErrDecryptionFailed   = errors.New("aead: decryption failed or authentication tag mismatch")
)

const (
	AEADNonceSize    = 12
	AEADTagSize      = 16
	AEADHeaderSize   = 2
	AEADMinCipherLen = AEADHeaderSize + AEADNonceSize + AEADTagSize
)

type AEADCipher struct {
	keys       map[uint16][32]byte
	keyVersion uint16
}

func NewAEADCipher(key []byte, version uint16) (*AEADCipher, error) {
	return NewAEADCipherKeyring(map[uint16][]byte{version: key}, version)
}

func NewAEADCipherKeyring(keys map[uint16][]byte, currentVersion uint16) (*AEADCipher, error) {
	if currentVersion == 0 {
		return nil, ErrUnknownKeyVersion
	}
	parsed := make(map[uint16][32]byte, len(keys))
	for version, key := range keys {
		if version == 0 || len(key) != 32 {
			return nil, ErrInvalidKeyLength
		}
		var fixed [32]byte
		copy(fixed[:], key)
		parsed[version] = fixed
	}
	if _, ok := parsed[currentVersion]; !ok {
		return nil, ErrUnknownKeyVersion
	}
	return &AEADCipher{keys: parsed, keyVersion: currentVersion}, nil
}

func (c *AEADCipher) KeyVersion() uint16 {
	return c.keyVersion
}

func (c *AEADCipher) Encrypt(plaintext []byte, aad []byte) ([]byte, error) {
	key := c.keys[c.keyVersion]
	block, err := aes.NewCipher(key[:])
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
	binary.BigEndian.PutUint16(out[:AEADHeaderSize], c.keyVersion)
	copy(out[AEADHeaderSize:], nonce)
	return gcm.Seal(out, nonce, plaintext, aad), nil
}

func openWithKey(key [32]byte, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func (c *AEADCipher) Decrypt(ciphertext []byte, aad []byte) ([]byte, error) {
	if len(ciphertext) >= AEADMinCipherLen {
		version := binary.BigEndian.Uint16(ciphertext[:AEADHeaderSize])
		if key, ok := c.keys[version]; ok {
			if plain, err := openWithKey(key, ciphertext[AEADHeaderSize:AEADHeaderSize+AEADNonceSize], ciphertext[AEADHeaderSize+AEADNonceSize:], aad); err == nil {
				return plain, nil
			}
			return nil, ErrDecryptionFailed
		}
	}
	// Compatibility for legacy one-byte version envelopes.
	if len(ciphertext) >= 1+AEADNonceSize+AEADTagSize {
		if key, ok := c.keys[uint16(ciphertext[0])]; ok {
			if plain, err := openWithKey(key, ciphertext[1:1+AEADNonceSize], ciphertext[1+AEADNonceSize:], aad); err == nil {
				return plain, nil
			}
		}
	}
	// Compatibility for legacy raw nonce envelopes, attempted only with the current key.
	if len(ciphertext) >= AEADNonceSize+AEADTagSize {
		if plain, err := openWithKey(c.keys[c.keyVersion], ciphertext[:AEADNonceSize], ciphertext[AEADNonceSize:], aad); err == nil {
			return plain, nil
		}
		return nil, ErrDecryptionFailed
	}
	return nil, ErrCiphertextTooShort
}
