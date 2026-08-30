package memory

import (
	"time"

	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

// SeedUserForTest creates an active user with credentials and privacy settings for testing
func (m *MemoryStore) SeedUserForTest(userID, handle, email string, now time.Time) (*domain.User, *domain.UserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := config.DefaultConfig()
	emailHash := crypto.HMACSHA256(cfg.EmailLookupKeys.Current(), []byte(email))
	var h *string
	visibility := domain.LeaderboardVisibilityPrivate
	if handle != "" {
		h = &handle
	}

	u := domain.User{
		UserID:                userID,
		EmailLookupHash:       &emailHash,
		EmailCiphertext:       []byte(email),
		Handle:                h,
		DisplayName:           "Test User",
		AccountStatus:         domain.AccountStatusActive,
		LeaderboardVisibility: visibility,
		TimezoneName:          "UTC",
		Locale:                "en-US",
		OnboardingCompletedAt: &now,
		ProfileVersion:        1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	cred := domain.UserPasswordCredential{
		UserID:            userID,
		PasswordHash:      "hash",
		CredentialVersion: 1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	priv := domain.UserPrivacySettings{
		UserID:                userID,
		PublicProfileEnabled:  false,
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
		ShowBio:               false,
		ShowTokenTotal:        false,
		ShowTrends:            false,
		ShowActivityCalendar:  false,
		ShowAgentBreakdown:    false,
		ShowSkillRanking:      false,
		ShowAchievements:      false,
		PrivacyVersion:        1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	sessionTokenHash := crypto.HMACSHA256(cfg.SessionKeys.Current(), []byte("test-session-token-"+userID))
	csrfTokenHash := crypto.HMACSHA256(cfg.CSRFKeys.Current(), []byte("test-csrf-token-"+userID))

	sess := domain.UserSession{
		SessionID:         "ses_" + userID,
		UserID:            userID,
		SessionTokenHash:  sessionTokenHash,
		CSRFTokenHash:     csrfTokenHash,
		CredentialVersion: 1,
		SessionStatus:     domain.SessionStatusActive,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(365 * 24 * time.Hour),
		AbsoluteExpiresAt: now.Add(365 * 24 * time.Hour),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	m.users[userID] = &u
	m.userCredentials[userID] = &cred
	m.privacySettings[userID] = &priv
	m.sessions[sess.SessionID] = &sess

	if handle != "" {
		pub := domain.PublicUserProfile{
			UserID:               userID,
			Handle:               handle,
			DisplayName:          u.DisplayName,
			ProfileStatus:        domain.ProfileStatusHidden,
			ShowBio:              false,
			ShowTokenTotal:       false,
			ShowTrends:           false,
			ShowActivityCalendar: false,
			ShowAgentBreakdown:   false,
			ShowSkillRanking:     false,
			ShowAchievements:     false,
			SourceProfileVersion: 1,
			SourcePrivacyVersion: 1,
			ProjectionVersion:    1,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		m.publicProfiles[userID] = &pub
	}

	return &u, &sess, nil
}

func (m *MemoryStore) SetAccountStatusForTest(userID string, status domain.AccountStatus, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[userID]
	if !ok {
		return domain.ErrNotFound
	}
	user.AccountStatus = status
	user.UpdatedAt = now
	return nil
}

func (m *MemoryStore) ResetCredentialFailuresForTest(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if credential, ok := m.userCredentials[userID]; ok {
		credential.FailedLoginCount = 0
		credential.LockedUntil = nil
		credential.LastFailedLoginAt = nil
	}
}

func (m *MemoryStore) CompleteRegistrationTxInputHelper(in store.RegistrationTxInput) (*domain.UserSession, error) {
	return m.CompleteRegistrationTx(nil, in)
}

func (m *MemoryStore) SeedUserMetricsFixture(userID string, fixture UserMetricFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userMetricFixtures[userID] = fixture
}

func (m *MemoryStore) SeedUserTrendFixture(userID string, points []UserTrendFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userTrendFixtures[userID] = points
}

func (m *MemoryStore) SeedUserSkillFixture(userID string, skills []UserSkillFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userSkillFixtures[userID] = skills
}

func (m *MemoryStore) SeedLeaderboardSnapshot(snapshot domain.LeaderboardResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapCopy := snapshot
	m.publishedSnapshots[snapshot.SnapshotID] = &snapCopy
	m.publishedSnapshots[snapshot.BoardKey+":"+snapshot.Window+":"+snapshot.Metric] = &snapCopy
}

func (m *MemoryStore) SeedInstallation(inst domain.Installation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	iCopy := inst
	m.installations[inst.InstallationID] = &iCopy
}

func (m *MemoryStore) SeedExportJob(job domain.DataExportJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	jCopy := job
	m.exportJobs[job.ExportID] = &jCopy
}

func (m *MemoryStore) SeedDeletionRequest(req domain.DataDeletionRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rCopy := req
	m.deletionRequests[req.RequestID] = &rCopy
}
