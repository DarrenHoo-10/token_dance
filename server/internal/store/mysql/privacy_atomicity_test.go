package mysql

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestMySQL_USR021_AccountDeletionAtomicallyHidesPublicProjection(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()
	userID := "usr_usr021_mysql_atomic"
	handle := "usr021_mysql_atomic"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (
			user_id, auth_subject_hash, handle, display_name, account_status,
			leaderboard_visibility, timezone_name, locale, onboarding_completed_at,
			profile_version, created_at, updated_at
		) VALUES (?, UNHEX(SHA2(?, 256)), ?, 'USR 021 MySQL', 'active',
			'public', 'UTC', 'en-US', ?, 1, ?, ?)`,
		userID, "subject:"+userID, handle, now, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_privacy_settings (
			user_id, public_profile_enabled, show_bio, show_token_total,
			privacy_version, created_at, updated_at
		) VALUES (?, TRUE, TRUE, TRUE, 1, ?, ?)`, userID, now, now); err != nil {
		t.Fatalf("seed privacy: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO public_user_profiles (
			user_id, handle, display_name, profile_status, show_bio, show_token_total,
			source_profile_version, source_privacy_version, projection_version,
			published_at, created_at, updated_at
		) VALUES (?, ?, 'USR 021 MySQL', 'published', TRUE, TRUE, 1, 1, 1, ?, ?, ?)`,
		userID, handle, now, now, now); err != nil {
		t.Fatalf("seed public projection: %v", err)
	}
	if _, err := st.Privacy().GetPublicProfileByHandle(ctx, handle, now); err != nil {
		t.Fatalf("published profile not readable before deletion: %v", err)
	}

	// Hold the production transaction open while readers run. Uncommitted user/profile
	// changes remain invisible; after commit no new read may observe the profile.
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER usr021_delay_profile_hide
		BEFORE UPDATE ON public_user_profiles
		FOR EACH ROW SET @usr021_delay = SLEEP(0.15)`); err != nil {
		t.Fatalf("create overlap trigger: %v", err)
	}

	var transitionRunning atomic.Bool
	var transitionCommitted atomic.Bool
	var readsDuring atomic.Int64
	var readsAfter atomic.Int64
	var visibleAfter atomic.Int64
	ready := make(chan struct{}, 4)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			ready <- struct{}{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				startedAfterCommit := transitionCommitted.Load()
				startedDuring := transitionRunning.Load() && !startedAfterCommit
				_, err := st.Privacy().GetPublicProfileByHandle(ctx, handle, time.Now().UTC())
				if startedDuring {
					readsDuring.Add(1)
				}
				if startedAfterCommit {
					readsAfter.Add(1)
					if err == nil {
						visibleAfter.Add(1)
					}
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-ready
	}

	transitionRunning.Store(true)
	cancelBefore := now.Add(24 * time.Hour)
	req := domain.DataDeletionRequest{
		RequestID:       "del_usr021_mysql_atomic",
		UserID:          &userID,
		DeletionScope:   "account",
		RequestStatus:   domain.DeletionStatusPending,
		Phase:           "queued",
		CancelBefore:    &cancelBefore,
		RequestedAt:     now,
		ScopeFilterJSON: map[string]interface{}{},
	}
	if _, err := st.Privacy().RequestDeletionTx(ctx, req, domain.UserSecurityEvent{}, now); err != nil {
		close(stop)
		readers.Wait()
		t.Fatalf("request account deletion: %v", err)
	}
	transitionCommitted.Store(true)

	deadline := time.Now().Add(2 * time.Second)
	for readsAfter.Load() < 50 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	readers.Wait()

	if readsDuring.Load() == 0 {
		t.Fatal("production deletion transaction did not overlap a public-profile read")
	}
	if readsAfter.Load() < 50 {
		t.Fatalf("insufficient post-commit reads: %d", readsAfter.Load())
	}
	if visibleAfter.Load() != 0 {
		t.Fatalf("public profile visible after deletion commit in %d reads", visibleAfter.Load())
	}
	if _, err := st.Privacy().GetPublicProfileByHandle(ctx, handle, time.Now().UTC()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected hidden public projection after commit, got %v", err)
	}
	t.Logf("MySQL transaction overlapped reads=%d post-commit reads=%d", readsDuring.Load(), readsAfter.Load())
}
