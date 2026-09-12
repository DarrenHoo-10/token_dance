package worker

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

func TestDeviceOwnedHistoricalReconstructionAndBinding(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx := context.Background()
	now := w.clk.Now()
	a, b, device := "usr_rebuild_a", "usr_rebuild_b", "ins_rebuild_a"
	seedAggUserInstall(t, st, a, device, now)
	seedAggUserInstall(t, st, b, "ins_rebuild_b", now)
	event := makeAggEvent(t, "historical-rebuild", now.AddDate(0, 0, -45).UnixMilli(), nil)
	commit := func(user, nonce string, version uint64, rebuild bool) v2.AckResult {
		result, err := st.Ingest().CommitTelemetryEventsV2(ctx, domain.TelemetryEventsV2Input{
			InstallationID: device, UserID: user, BindingStatusVersion: version, Reconstruction: rebuild,
			NonceHash: crypto.SHA256([]byte(nonce)), NonceExpiresAt: now.Add(time.Minute), RequestID: nonce,
			Events: []v2.EventEnvelope{event}, ReceivedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Acks[0].Result
	}
	if result := commit(a, "daily", 1, false); result != v2.AckResultDiscarded {
		t.Fatal(result)
	}
	if result := commit(a, "rebuild", 1, true); result != v2.AckResultAccepted {
		t.Fatal(result)
	}
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Fatal(err)
	}
	sum := func(user string) int {
		var total int
		if err := st.DB().QueryRow("SELECT COALESCE(SUM(exact_token_total),0) FROM bound_telemetry_model_metrics WHERE user_id=? AND grain='day'", user).Scan(&total); err != nil {
			t.Fatal(err)
		}
		return total
	}
	if sum(a) != 10 || sum(b) != 0 {
		t.Fatal("initial ownership")
	}
	if _, err := st.Device().RebindInstallationTx(ctx, device, b, now); err == nil {
		t.Fatal("must unbind before transferring an owned device")
	}
	if _, err := st.Device().RevokeInstallation(ctx, device, a, now); err != nil {
		t.Fatal(err)
	}
	if sum(a) != 0 {
		t.Fatal("unbound device still contributes")
	}
	rebound, err := st.Device().RebindInstallationTx(ctx, device, b, now)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.InstallationID != device || sum(b) != 10 || sum(a) != 0 {
		t.Fatal("device history did not follow binding")
	}
	if result := commit(b, "replayed-after-bind", rebound.StatusVersion, true); result != v2.AckResultDuplicate {
		t.Fatal(result)
	}
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM telemetry_events WHERE installation_id=?", device).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || sum(b) != 10 {
		t.Fatal("reconstruction duplicated device usage")
	}
}
