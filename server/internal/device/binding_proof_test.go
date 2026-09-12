package device

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/store/memory"
)

func TestRebindPreservesDeviceAndRequiresPrivateKey(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	st := memory.NewMemoryStore()
	st.SeedUserForTest("usr_proof_a", "proofa", "a@example.test", now)
	st.SeedUserForTest("usr_proof_b", "proofb", "b@example.test", now)
	svc := NewService(st, config.DefaultConfig(), clock.NewMockClock(now))
	pub, key, _ := ed25519.GenerateKey(nil)
	in := ClaimInput{PublicKey: hex.EncodeToString(pub), OSType: "windows", Architecture: "x86_64", CollectorVersion: "0.1.27"}
	first, err := svc.RegisterInstallation(ctx, "usr_proof_a", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterInstallation(ctx, "usr_proof_b", in); err == nil {
		t.Fatal("active device transferred")
	}
	if _, err := st.RevokeInstallation(ctx, first.InstallationID, "usr_proof_a", now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterInstallation(ctx, "usr_proof_b", in); err == nil {
		t.Fatal("public key alone claimed history")
	}
	in.ProofTimestamp = strconv.FormatInt(now.Unix(), 10)
	message := "tokendance-device-binding\nregister:usr_proof_b\n" + in.PublicKey + "\n" + in.ProofTimestamp
	in.ProofSignature = hex.EncodeToString(ed25519.Sign(key, []byte(message)))
	if verifyBindingProof(in, "register:usr_proof_a", now) {
		t.Fatal("proof accepted for another user")
	}
	if verifyBindingProof(in, "register:usr_proof_b", now.Add(6*time.Minute)) {
		t.Fatal("expired proof accepted")
	}
	rebound, err := svc.RegisterInstallation(ctx, "usr_proof_b", in)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.InstallationID != first.InstallationID || rebound.UserID != "usr_proof_b" {
		t.Fatal("identity changed")
	}
}
