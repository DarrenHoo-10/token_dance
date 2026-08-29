package memory

import (
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

// SeedUserForTest creates an active user with credentials and privacy settings for testing
func (m *MemoryStore) SeedUserForTest(userID, handle, email string, now time.Time) (*domain.User, *domain.UserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	emailHash := crypto.SHA256([]byte(email))
	var h *string
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
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
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
		UserID:         userID,
		PrivacyVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	sessionTokenHash := crypto.SHA256([]byte("test-session-token-" + userID))
	csrfTokenHash := crypto.SHA256([]byte("test-csrf-token-" + userID))

	sess := domain.UserSession{
		SessionID:         "ses_" + userID,
		UserID:            userID,
		SessionTokenHash:  sessionTokenHash,
		CSRFTokenHash:     csrfTokenHash,
		CredentialVersion: 1,
		SessionStatus:     domain.SessionStatusActive,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(24 * time.Hour),
		AbsoluteExpiresAt: now.Add(30 * 24 * time.Hour),
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
			ProfileStatus:        domain.ProfileStatusPublished,
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

func (m *MemoryStore) CompleteRegistrationTxInputHelper(in store.RegistrationTxInput) (*domain.UserSession, error) {
	return m.CompleteRegistrationTx(nil, in)
}
