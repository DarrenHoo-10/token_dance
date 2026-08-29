package export

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store/memory"
)

func TestUSR102_ExportAuthorizationAndSignedURL(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	storage := provider.NewMemoryObjectStorage("")
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk, storage)

	userID := "usr_export_service_test"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "exportuser", "exp@tokendance.dev", now)

	// 1. Create Job with Idempotency Key -> Job is PENDING
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
	if job1.JobStatus != domain.ExportJobStatusPending {
		t.Fatalf("expected job status pending, got %s", job1.JobStatus)
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

	// 5. Attempt download before completed -> 400 notReady
	_, err = svc.GetDownloadURL(ctx, job1.ExportID, userID)
	if err == nil {
		t.Fatalf("expected error when downloading pending job")
	}

	// 6. Complete job in store & put object in storage
	objKey := "exports/" + userID + "/" + job1.ExportID + ".csv"
	fileData := []byte("date,tokens\n2026-08-30,1000\n")
	fileSha := sha256.Sum256(fileData)
	_ = storage.PutObject(ctx, objKey, bytes.NewReader(fileData), int64(len(fileData)), "text/csv")
	_ = st.CompleteJob(ctx, job1.ExportID, "test_worker", objKey, fileSha, uint64(len(fileData)), now)

	// 7. Get Signed Download URL -> SUCCEEDS with signed URL expiring in 60s
	dl, err := svc.GetDownloadURL(ctx, job1.ExportID, userID)
	if err != nil {
		t.Fatalf("failed to get download URL: %v", err)
	}
	if dl.DownloadURL == "" || dl.ExpiresAt == "" {
		t.Errorf("download response missing URL or expiry: %+v", dl)
	}

	// 8. Access other user's export -> 404
	_, err = svc.GetJob(ctx, job1.ExportID, "usr_other_user")
	if err == nil {
		t.Errorf("expected 404 when accessing other user's export job")
	}
}
