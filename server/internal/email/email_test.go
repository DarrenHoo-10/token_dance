package email

import (
	"context"
	"testing"
	"time"
)

func TestDeliverySink_DeterministicRetrieval(t *testing.T) {
	sink := NewDeliverySink()
	ctx := context.Background()

	msg1 := Message{
		EmailID:     "eml_01",
		Recipient:   "alice@example.com",
		TemplateKey: "auth.register_code",
		Locale:      "en-US",
		PayloadJSON: `{"code":"111222"}`,
		CreatedAt:   time.Now().UTC(),
	}

	msg2 := Message{
		EmailID:     "eml_02",
		Recipient:   "bob@example.com",
		TemplateKey: "auth.register_code",
		Locale:      "en-US",
		PayloadJSON: `{"code":"333444"}`,
		CreatedAt:   time.Now().UTC(),
	}

	msg3 := Message{
		EmailID:     "eml_03",
		Recipient:   "alice@example.com",
		TemplateKey: "auth.password_reset_code",
		Locale:      "en-US",
		PayloadJSON: `{"code":"555666"}`,
		CreatedAt:   time.Now().UTC(),
	}

	_, _ = sink.Send(ctx, msg1)
	_, _ = sink.Send(ctx, msg2)
	_, _ = sink.Send(ctx, msg3)

	if code := sink.LatestCode("alice@example.com"); code != "555666" {
		t.Fatalf("expected latest code 555666 for alice, got %s", code)
	}

	if code := sink.LatestCode("bob@example.com"); code != "333444" {
		t.Fatalf("expected latest code 333444 for bob, got %s", code)
	}

	if code := sink.LatestCode(""); code != "555666" {
		t.Fatalf("expected latest overall code 555666, got %s", code)
	}

	if msg := sink.LatestMessage("bob@example.com"); msg == nil || msg.EmailID != "eml_02" {
		t.Fatalf("expected bob's message eml_02, got %v", msg)
	}

	sink.Clear()
	if code := sink.LatestCode("alice@example.com"); code != "" {
		t.Fatalf("expected empty code after clear, got %s", code)
	}
}
