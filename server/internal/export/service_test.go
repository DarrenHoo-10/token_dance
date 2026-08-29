package export

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/store/memory"
)

func TestExportService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	userID := "usr_export_service_test"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "exportuser", "exp@tokendance.dev", now)

	// 1. Create Job with Idempotency Key
	in := CreateExportInput{
		IdempotencyKey: "idemp_svc_001",
		Scope:          "summary",
		Format:         "csv",
		Filter:         map[string]interface{}{"range": "30d"},
	}

	job1, err := svc.CreateJob(ctx, userID, in)
	if err != nil {
		t.Fatalf("failed to create export job: %v", err)
	}
	if job1.ExportID == "" {
		t.Fatalf("expected non-empty export ID")
	}

	// 2. Same Idempotency Key and same payload -> Idempotent
	jobSame, err := svc.CreateJob(ctx, userID, in)
	if err != nil {
		t.Fatalf("failed to re-create export job idempotently: %v", err)
	}
	if jobSame.ExportID != job1.ExportID {
		t.Errorf("expected same export ID %s, got %s", job1.ExportID, jobSame.ExportID)
	}

	// 3. Same Idempotency Key with different payload -> 409 Conflict
	inDiff := in
	inDiff.Scope = "activity"
	_, err = svc.CreateJob(ctx, userID, inDiff)
	if err == nil {
		t.Fatalf("expected conflict error for reused idempotency key with different payload")
	}

	// 4. List Jobs
	jobs, err := svc.ListJobs(ctx, userID)
	if err != nil {
		t.Fatalf("failed to list export jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("expected 1 job in list, got %d", len(jobs))
	}

	// 5. Get Job & Signed Download URL
	jobGot, err := svc.GetJob(ctx, job1.ExportID, userID)
	if err != nil || jobGot.ExportID != job1.ExportID {
		t.Fatalf("failed to get export job: %v", err)
	}

	dl, err := svc.GetDownloadURL(ctx, job1.ExportID, userID)
	if err != nil {
		t.Fatalf("failed to get download URL: %v", err)
	}
	if dl.DownloadURL == "" || dl.ExpiresAt == "" {
		t.Errorf("download response missing URL or expiry: %+v", dl)
	}

	// 6. Access other user's export -> 404
	_, err = svc.GetJob(ctx, job1.ExportID, "usr_other_user")
	if err == nil {
		t.Errorf("expected 404 when accessing other user's export job")
	}
}
