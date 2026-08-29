package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"tokendance/internal/crypto"
)

var (
	ErrTransient       = errors.New("transient provider error")
	ErrPermanent       = errors.New("permanent provider error")
	ErrExpired         = errors.New("message expired")
	ErrObjectNotFound  = errors.New("object not found in storage")
	ErrInvalidPayload  = errors.New("invalid payload data")
)

type ProviderError struct {
	Code      string
	Message   string
	Transient bool
	Err       error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

func (e *ProviderError) IsTransient() bool {
	return e.Transient
}

func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTransient) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Transient
	}
	return false
}

// OutboxEmail encapsulates email details dispatched via EmailProvider
type OutboxEmail struct {
	EmailID              string
	UserID               *string
	ChallengeID          *string
	IdempotencyKey       [32]byte
	TemplateKey          string
	Locale               string
	RecipientCiphertext  []byte
	PayloadCiphertext    []byte
	EncryptionKeyVersion uint16
	DeliveryStatus       string
	AttemptCount         uint16
	NextAttemptAt        time.Time
	ExpiresAt            time.Time
}

// SendEmailResult holds metadata returned by provider on success
type SendEmailResult struct {
	ProviderMessageID string
	SentAt            time.Time
}

// EmailProvider defines the interface for delivering outbox emails
type EmailProvider interface {
	SendEmail(ctx context.Context, providerRequestID string, msg OutboxEmail) (*SendEmailResult, error)
}

// MemoryEmailProvider provides an in-memory and mockable EmailProvider
type MemoryEmailProvider struct {
	mu           sync.Mutex
	sentMessages map[string]OutboxEmail
	requestIDs   map[string]string // providerRequestID -> messageID
	failFunc     func(providerRequestID string, msg OutboxEmail) error
	cipher       *crypto.AEADCipher
}

func NewMemoryEmailProvider() *MemoryEmailProvider {
	return &MemoryEmailProvider{
		sentMessages: make(map[string]OutboxEmail),
		requestIDs:   make(map[string]string),
	}
}

func (p *MemoryEmailProvider) SetCipher(c *crypto.AEADCipher) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cipher = c
}

func (p *MemoryEmailProvider) SetFailFunc(fn func(providerRequestID string, msg OutboxEmail) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failFunc = fn
}

func (p *MemoryEmailProvider) SendEmail(ctx context.Context, providerRequestID string, msg OutboxEmail) (*SendEmailResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failFunc != nil {
		if err := p.failFunc(providerRequestID, msg); err != nil {
			return nil, err
		}
	}

	// Check if already sent with same providerRequestID (idempotent replay)
	if existingID, exists := p.requestIDs[providerRequestID]; exists {
		return &SendEmailResult{
			ProviderMessageID: existingID,
			SentAt:            time.Now().UTC(),
		}, nil
	}

	msgID := fmt.Sprintf("pmsg_%s_%d", msg.EmailID, time.Now().UnixNano())
	p.sentMessages[msgID] = msg
	p.requestIDs[providerRequestID] = msgID

	return &SendEmailResult{
		ProviderMessageID: msgID,
		SentAt:            time.Now().UTC(),
	}, nil
}

func (p *MemoryEmailProvider) GetSentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sentMessages)
}

func (p *MemoryEmailProvider) LatestCode(recipient string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, msg := range p.sentMessages {
		rcpt := string(msg.RecipientCiphertext)
		if p.cipher != nil {
			if plain, err := p.cipher.Decrypt(msg.RecipientCiphertext, []byte("email_outbox.recipient")); err == nil {
				rcpt = string(plain)
			}
		}

		if recipient == "" || rcpt == recipient {
			payloadBytes := msg.PayloadCiphertext
			if p.cipher != nil {
				if plain, err := p.cipher.Decrypt(msg.PayloadCiphertext, []byte("email_outbox.payload")); err == nil {
					payloadBytes = plain
				}
			}

			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(payloadBytes, &payload); err == nil && payload.Code != "" {
				return payload.Code
			}
		}
	}
	return ""
}

func (p *MemoryEmailProvider) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sentMessages = make(map[string]OutboxEmail)
	p.requestIDs = make(map[string]string)
}

// ObjectMeta represents object-storage metadata
type ObjectMeta struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// ObjectStorage defines interface for object storage operations
type ObjectStorage interface {
	PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error
	HeadObject(ctx context.Context, key string) (*ObjectMeta, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
	PresignDownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignUploadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	DeleteObject(ctx context.Context, key string) error
}

type storedObject struct {
	meta ObjectMeta
	data []byte
}

// MemoryObjectStorage is a thread-safe in-memory ObjectStorage implementation
type MemoryObjectStorage struct {
	mu      sync.RWMutex
	objects map[string]*storedObject
	secret  []byte
	baseURL string
}

func NewMemoryObjectStorage(baseURL string) *MemoryObjectStorage {
	if baseURL == "" {
		baseURL = "https://storage.tokendance.dev"
	}
	return &MemoryObjectStorage{
		objects: make(map[string]*storedObject),
		secret:  []byte("tokendance-object-storage-secret-key-32b"),
		baseURL: baseURL,
	}
}

func (s *MemoryObjectStorage) PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	buf := new(bytes.Buffer)
	if data != nil {
		if _, err := io.Copy(buf, data); err != nil {
			return fmt.Errorf("failed to read data stream: %w", err)
		}
	}
	bytesData := buf.Bytes()
	actualSize := int64(len(bytesData))
	if size > 0 && actualSize != size {
		return fmt.Errorf("size mismatch: expected %d, got %d", size, actualSize)
	}

	h := sha256.Sum256(bytesData)
	etag := hex.EncodeToString(h[:])

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	s.objects[key] = &storedObject{
		meta: ObjectMeta{
			Key:          key,
			Size:         actualSize,
			ContentType:  contentType,
			ETag:         etag,
			LastModified: time.Now().UTC(),
		},
		data: bytesData,
	}
	return nil
}

func (s *MemoryObjectStorage) HeadObject(ctx context.Context, key string) (*ObjectMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, exists := s.objects[key]
	if !exists {
		return nil, ErrObjectNotFound
	}
	metaCopy := obj.meta
	return &metaCopy, nil
}

func (s *MemoryObjectStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, exists := s.objects[key]
	if !exists {
		return nil, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (s *MemoryObjectStorage) PresignDownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	exp := time.Now().UTC().Add(ttl).Unix()
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(fmt.Sprintf("GET:%s:%d", key, exp)))
	sig := hex.EncodeToString(mac.Sum(nil))

	u := fmt.Sprintf("%s/%s?exp=%d&sig=%s", s.baseURL, url.PathEscape(key), exp, sig)
	return u, nil
}

func (s *MemoryObjectStorage) PresignUploadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	exp := time.Now().UTC().Add(ttl).Unix()
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(fmt.Sprintf("PUT:%s:%d", key, exp)))
	sig := hex.EncodeToString(mac.Sum(nil))

	u := fmt.Sprintf("%s/%s?exp=%d&sig=%s", s.baseURL, url.PathEscape(key), exp, sig)
	return u, nil
}

func (s *MemoryObjectStorage) DeleteObject(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.objects, key)
	return nil
}

func (s *MemoryObjectStorage) HasObject(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[key]
	return ok
}
