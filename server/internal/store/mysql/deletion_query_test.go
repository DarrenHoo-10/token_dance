package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/crypto"
)

func TestMySQL_GetDeletionRequestWithNullScopeFilter(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	userID := "usr_deletion_status_owner"
	requestID := "del_deletion_status_query"
	subjectHash := crypto.SHA256([]byte("deletion-status-owner"))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, created_at, updated_at)
		VALUES (?, ?, 'Deletion Owner', 'active', 'private', 'UTC', ?, ?)`,
		userID, subjectHash[:], now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO data_deletion_requests (
			request_id, user_id, deletion_scope, scope_filter_json, request_status,
			phase, progress_cursor, requested_at, active_account_key
		) VALUES (?, ?, 'all_usage', NULL, 'pending', 'queued', 0, ?, NULL)`,
		requestID, userID, now); err != nil {
		t.Fatal(err)
	}
	got, err := st.Privacy().GetDeletionRequest(ctx, requestID, userID)
	if err != nil {
		t.Fatalf("owner-scoped deletion status failed for NULL filter: %v", err)
	}
	if got.RequestID != requestID || got.UserID == nil || *got.UserID != userID || len(got.ScopeFilterJSON) != 0 {
		t.Fatalf("unexpected deletion request: %+v", got)
	}
}
