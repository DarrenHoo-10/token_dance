package ingest

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/protocol"
)

func mappingEnvelope(payload any) protocol.EventEnvelope {
	encoded := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hmac := protocol.HmacSha256("hmac-sha256:" + encoded)
	return protocol.EventEnvelope{
		SchemaVersion:  "1.0.0",
		EventID:        protocol.Base64Url32(encoded),
		AdapterID:      "adapter",
		AdapterVersion: "1.0.0",
		AgentID:        "agent",
		InstallationID: "installation",
		OccurredAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Source: protocol.EventSource{
			Kind:               protocol.SourceKindRuntimeStream,
			CursorHMAC:         hmac,
			RawFingerprintHMAC: hmac,
		},
		Accuracy: protocol.AccuracyExact,
		Payload:  protocol.EventPayload{Value: payload},
	}
}

func TestEnvelopeToEventMapsAllIngestFields(t *testing.T) {
	u64 := func(value string) *protocol.UInt64String { v := protocol.UInt64String(value); return &v }
	text := func(value string) *string { return &value }
	encoded := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hmac := protocol.HmacSha256("hmac-sha256:" + encoded)

	tests := []struct {
		name    string
		payload any
		check   func(*testing.T, any)
	}{
		{name: "tool", payload: protocol.ToolInvokedPayload{Type: protocol.EventTypeToolInvoked, ToolCategory: "shell", Success: true, DurationMs: u64("3")}, check: func(t *testing.T, value any) {
			e := value.(*string)
			if *e != "shell" {
				t.Fatalf("tool category=%q", *e)
			}
		}},
		{name: "skill", payload: protocol.SkillInvokedPayload{Type: protocol.EventTypeSkillInvoked, SkillKey: hmac, InvokeType: protocol.SkillInvokeTypeNative, PluginKey: &hmac, Success: true}, check: func(t *testing.T, value any) {
			e := value.([3]bool)
			if !e[0] || !e[1] || !e[2] {
				t.Fatalf("skill fields=%v", e)
			}
		}},
		{name: "cost", payload: protocol.CostRecordedPayload{Type: protocol.EventTypeCostRecorded, Amount: protocol.DecimalString("1.25000000"), Currency: "USD", Source: protocol.CostSourceProviderReported, DiscountAmount: func() *protocol.DecimalString { v := protocol.DecimalString("0.25000000"); return &v }()}, check: func(t *testing.T, value any) {
			e := value.([4]string)
			if e != [4]string{"1.25000000", "USD", "provider_reported", "0.25000000"} {
				t.Fatalf("cost fields=%v", e)
			}
		}},
		{name: "parent-child", payload: protocol.AgentSpawnedPayload{Type: protocol.EventTypeAgentSpawned, ChildSessionHash: hmac, SpawnedAgentType: "subagent"}, check: func(t *testing.T, value any) {
			e := value.([2]bool)
			if !e[0] || !e[1] {
				t.Fatalf("parent-child fields=%v", e)
			}
		}},
		{name: "code", payload: protocol.CodeChangedPayload{Type: protocol.EventTypeCodeChanged, AddedLines: "7", RemovedLines: "2", GeneratedLines: u64("6"), AcceptedLines: u64("5"), FileCount: 3, Language: text("go")}, check: func(t *testing.T, value any) {
			e := value.([6]uint64)
			if e != [6]uint64{7, 2, 6, 5, 3, 1} {
				t.Fatalf("code fields=%v", e)
			}
		}},
		{name: "token-tool", payload: protocol.ModelUsageRecordedPayload{Type: protocol.EventTypeModelUsageRecorded, ProviderID: "provider", ModelID: "model", Tokens: protocol.TokenUsage{ToolTokens: u64("13")}}, check: func(t *testing.T, value any) {
			if value.(uint64) != 13 {
				t.Fatalf("token tool=%d", value.(uint64))
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped, err := envelopeToEvent(mappingEnvelope(test.payload), "batch", "installation", "user", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			switch test.name {
			case "tool":
				test.check(t, mapped.ToolCategory)
			case "skill":
				test.check(t, [3]bool{mapped.SkillKey != nil, mapped.SkillInvokeType != nil && *mapped.SkillInvokeType == "native", mapped.PluginKey != nil})
			case "cost":
				test.check(t, [4]string{*mapped.CostAmount, *mapped.CostCurrency, *mapped.CostSource, *mapped.CostDiscountAmount})
			case "parent-child":
				test.check(t, [2]bool{mapped.ChildSessionHash != nil, mapped.SpawnedAgentType != nil && *mapped.SpawnedAgentType == "subagent"})
			case "code":
				test.check(t, [6]uint64{*mapped.Event.CodeAddedLines, *mapped.Event.CodeDeletedLines, *mapped.Event.CodeGeneratedLines, *mapped.Event.CodeAcceptedLines, uint64(*mapped.Event.CodeFileCount), boolToUint64(mapped.CodeLanguage != nil && *mapped.CodeLanguage == "go")})
			case "token-tool":
				test.check(t, *mapped.Event.TokenTool)
			}
		})
	}
}

func TestEnvelopeToEventMapsTypedSessionAndTurnFields(t *testing.T) {
	expectedWorkspace := bytes.Repeat([]byte{7}, 32)
	encoded := base64.RawURLEncoding.EncodeToString(expectedWorkspace)
	workspace := protocol.HmacSha256("hmac-sha256:" + encoded)
	trigger := protocol.TurnTriggerSubagent

	tests := []struct {
		name    string
		payload any
		check   func(*testing.T, any)
	}{
		{name: "workspace hash", payload: protocol.SessionStartedPayload{Type: protocol.EventTypeSessionStarted, WorkspaceHash: &workspace}, check: func(t *testing.T, value any) {
			got := value.(*[32]byte)
			if got == nil || !bytes.Equal(got[:], expectedWorkspace) {
				t.Fatalf("workspace hash=%v", value)
			}
		}},
		{name: "session end reason", payload: protocol.SessionEndedPayload{Type: protocol.EventTypeSessionEnded, Reason: protocol.SessionEndReasonTimeout}, check: func(t *testing.T, value any) {
			if got := string(*value.(*domain.SessionEndReason)); got != "timeout" {
				t.Fatalf("session end reason=%q", got)
			}
		}},
		{name: "turn trigger", payload: protocol.TurnStartedPayload{Type: protocol.EventTypeTurnStarted, Trigger: &trigger}, check: func(t *testing.T, value any) {
			if got := string(*value.(*domain.TurnTrigger)); got != "subagent" {
				t.Fatalf("turn trigger=%q", got)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped, err := envelopeToEvent(mappingEnvelope(test.payload), "batch", "installation", "user", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			switch test.name {
			case "workspace hash":
				test.check(t, mapped.Event.WorkspaceHash)
			case "session end reason":
				test.check(t, mapped.Event.SessionEndReason)
			case "turn trigger":
				test.check(t, mapped.Event.TurnTrigger)
			}
		})
	}
}

func boolToUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}
