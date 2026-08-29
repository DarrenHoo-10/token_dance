package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTransient = errors.New("transient email delivery error")
	ErrPermanent = errors.New("permanent email delivery error")
	ErrExpired   = errors.New("email delivery expired")
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

// Message represents an email message to be sent or recorded
type Message struct {
	EmailID     string    `json:"emailId"`
	Recipient   string    `json:"recipient"`
	TemplateKey string    `json:"templateKey"`
	Locale      string    `json:"locale"`
	PayloadJSON string    `json:"payloadJson"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Provider defines the interface for delivering emails
type Provider interface {
	Send(ctx context.Context, msg Message) (providerMessageID string, err error)
}

// DeliverySink is a thread-safe in-memory sink for deterministic local and test delivery
type DeliverySink struct {
	mu       sync.RWMutex
	messages []Message
}

// NewDeliverySink creates a new empty DeliverySink
func NewDeliverySink() *DeliverySink {
	return &DeliverySink{
		messages: make([]Message, 0),
	}
}

// DefaultSink is a global delivery sink used in test/dev modes
var DefaultSink = NewDeliverySink()

// Send records the message in the sink
func (s *DeliverySink) Send(ctx context.Context, msg Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	return fmt.Sprintf("sink_msg_%s", msg.EmailID), nil
}

// LatestCode extracts the verification code from the most recent message for recipient
func (s *DeliverySink) LatestCode(recipient string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if recipient == "" || m.Recipient == recipient {
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(m.PayloadJSON), &payload); err == nil && payload.Code != "" {
				return payload.Code
			}
		}
	}
	return ""
}

// LatestMessage returns a copy of the most recent message for recipient
func (s *DeliverySink) LatestMessage(recipient string) *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if recipient == "" || m.Recipient == recipient {
			msgCopy := m
			return &msgCopy
		}
	}
	return nil
}

// AllMessages returns a snapshot of all messages in the sink
func (s *DeliverySink) AllMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make([]Message, len(s.messages))
	copy(copied, s.messages)
	return copied
}

// Clear clears all messages from the sink
func (s *DeliverySink) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = s.messages[:0]
}
