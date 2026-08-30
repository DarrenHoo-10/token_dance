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

func TestUSR021_MySQLAccountSuspensionAtomicallyHidesPublicProjection(t *testing.T) {
	runUSR021MySQLAtomicHide(t, "suspension", func(ctx context.Context, st *Store, userID string, now time.Time) error {
		return st.Privacy().SetAccountStatusTx(ctx, userID, domain.AccountStatusSuspended, now)
	}, true)
}

func TestUSR021_MySQLAccountDeletionAtomicallyHidesPublicProjection(t *testing.T) {
	runUSR021MySQLAtomicHide(t, "deletion", func(ctx context.Context, st *Store, userID string, now time.Time) error {
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
		_, err := st.Privacy().RequestDeletionTx(ctx, req, domain.UserSecurityEvent{}, now)
		return err
	}, false)
}

func runUSR021MySQLAtomicHide(
	t *testing.T,
	transitionName string,
	transition func(context.Context, *Store, string, time.Time) error,
	restore bool,
) {
	t.Helper()
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
		t.Fatalf("published profile not readable before %s: %v", transitionName, err)
	}

	// Keep the shipped MySQL transaction open while readers run. Before commit they
	// see the old state; every read started after commit must return ErrNotFound.
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
	var unexpectedAfter atomic.Int64
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
					switch {
					case err == nil:
						visibleAfter.Add(1)
					case !errors.Is(err, domain.ErrNotFound):
						unexpectedAfter.Add(1)
					}
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-ready
	}

	transitionRunning.Store(true)
	if err := transition(ctx, st, userID, now); err != nil {
		close(stop)
		readers.Wait()
		t.Fatalf("execute account %s: %v", transitionName, err)
	}
	transitionCommitted.Store(true)

	deadline := time.Now().Add(2 * time.Second)
	for readsAfter.Load() < 50 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	readers.Wait()

	if readsDuring.Load() == 0 {
		t.Fatalf("production %s transaction did not overlap a public-profile read", transitionName)
	}
	if readsAfter.Load() < 50 {
		t.Fatalf("insufficient post-commit reads after %s: %d", transitionName, readsAfter.Load())
	}
	if visibleAfter.Load() != 0 {
		t.Fatalf("public profile visible after %s commit in %d reads", transitionName, visibleAfter.Load())
	}
	if unexpectedAfter.Load() != 0 {
		t.Fatalf("post-commit %s reads returned %d errors other than ErrNotFound", transitionName, unexpectedAfter.Load())
	}
	if _, err := st.Privacy().GetPublicProfileByHandle(ctx, handle, time.Now().UTC()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected hidden public projection after %s commit, got %v", transitionName, err)
	}

	if restore {
		restoredAt := now.Add(time.Second)
		if err := st.Privacy().SetAccountStatusTx(ctx, userID, domain.AccountStatusActive, restoredAt); err != nil {
			t.Fatalf("restore active account: %v", err)
		}
		if _, err := st.Privacy().GetPublicProfileByHandle(ctx, handle, restoredAt); err != nil {
			t.Fatalf("active account did not republish enabled projection: %v", err)
		}
	}
	t.Logf("MySQL %s transaction overlapped reads=%d post-commit ErrNotFound reads=%d", transitionName, readsDuring.Load(), readsAfter.Load())
}
