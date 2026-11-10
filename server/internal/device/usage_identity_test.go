package device

import (
	"testing"
	"tokendance/internal/config"
	v2 "tokendance/internal/protocol/v2"
)

func TestMixedBatchUsageIdentityAlignment(t *testing.T) {
	cfg := config.DefaultConfig()
	s := &Service{cfg: cfg}
	identity := &v2.UsageIdentity{SessionID: "synthetic-session", MessageID: "synthetic-message"}
	events := []v2.EventEnvelope{
		{HarnessID: "codex"},
		{HarnessID: "claude-code", UsageIdentity: identity},
		{HarnessID: "codex"},
	}
	got := s.computeUsageIdentities(events)
	if len(got) != 3 || got[0].Hash != [32]byte{} || got[2].Hash != [32]byte{} || got[1].Hash == [32]byte{} {
		t.Fatalf("mixed batch identity alignment lost")
	}
	if got := s.computeUsageIdentities([]v2.EventEnvelope{{HarnessID: "codex"}}); got != nil {
		t.Fatal("absent identities must remain absent")
	}
}
