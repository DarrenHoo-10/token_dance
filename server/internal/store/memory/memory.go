package memory

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type UserMetricFixture struct {
	AggregationVersion   uint32
	CostAmount           *string
	CostCurrency         *string
	CostSupported        bool
	ExactTokenTotal      uint64
	DerivedTokenTotal    uint64
	CodeGeneratedLines   uint64
	TokenInputTotal      uint64
	TokenOutputTotal     uint64
	TokenCacheReadTotal  uint64
	TokenCacheWriteTotal uint64
	TokenReasoningTotal  uint64
	ActiveDurationMs     uint64
	MessageCount         uint64
	UserMessageCount     uint64
	ExtensionsSupported  bool
}

type UserSkillFixture struct {
	SkillID          string
	SkillKey         string
	SkillPublicName  string
	UseCount         uint64
	ActiveDays       int
	PublicUserCount  int
	SuccessRate      *float64
	PreviousDeltaPct *float64
}

type UserTrendFixture struct {
	Date             string
	AgentID          string
	ProviderID       string
	ModelID          string
	ExactTokens      uint64
	DerivedTokens    uint64
	InputTokens      uint64
	OutputTokens     uint64
	CacheReadTokens  uint64
	CacheWriteTokens uint64
	ReasoningTokens  uint64
}

type MemoryStore struct {
	mu sync.RWMutex

	FaultInjector func(operation string) error

	users              map[string]*domain.User
	userCredentials    map[string]*domain.UserPasswordCredential
	sessions           map[string]*domain.UserSession
	emailChallenges    map[string]*domain.EmailChallenge
	emailOutbox        map[string]*domain.EmailOutbox
	privacySettings    map[string]*domain.UserPrivacySettings
	publicProfiles     map[string]*domain.PublicUserProfile
	handleHistory      map[string]*domain.UserHandleHistory
	uploadObjects      map[string]*domain.UserUploadObject
	bindingChallenges  map[string]*domain.DeviceBindingChallenge
	installations      map[string]*domain.Installation
	ingestNonces       map[string]time.Time
	ingestBatches      map[string]*domain.IngestResult
	ingestBatchHashes  map[string][32]byte
	usageEvents        map[string]domain.UsageEvent
	securityEvents     []domain.UserSecurityEvent
	exportJobs         map[string]*domain.DataExportJob
	deletionRequests   map[string]*domain.DataDeletionRequest
	userMetricFixtures map[string]UserMetricFixture
	userSkillFixtures  map[string][]UserSkillFixture
	userTrendFixtures  map[string][]UserTrendFixture
	publishedSnapshots map[string]*domain.LeaderboardResponse
	buildingSnapshots  map[string]*domain.LeaderboardResponse
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:              make(map[string]*domain.User),
		userCredentials:    make(map[string]*domain.UserPasswordCredential),
		sessions:           make(map[string]*domain.UserSession),
		emailChallenges:    make(map[string]*domain.EmailChallenge),
		emailOutbox:        make(map[string]*domain.EmailOutbox),
		privacySettings:    make(map[string]*domain.UserPrivacySettings),
		publicProfiles:     make(map[string]*domain.PublicUserProfile),
		handleHistory:      make(map[string]*domain.UserHandleHistory),
		uploadObjects:      make(map[string]*domain.UserUploadObject),
		bindingChallenges:  make(map[string]*domain.DeviceBindingChallenge),
		installations:      make(map[string]*domain.Installation),
		ingestNonces:       make(map[string]time.Time),
		ingestBatches:      make(map[string]*domain.IngestResult),
		ingestBatchHashes:  make(map[string][32]byte),
		usageEvents:        make(map[string]domain.UsageEvent),
		securityEvents:     make([]domain.UserSecurityEvent, 0),
		exportJobs:         make(map[string]*domain.DataExportJob),
		deletionRequests:   make(map[string]*domain.DataDeletionRequest),
		userMetricFixtures: make(map[string]UserMetricFixture),
		userSkillFixtures:  make(map[string][]UserSkillFixture),
		userTrendFixtures:  make(map[string][]UserTrendFixture),
		publishedSnapshots: make(map[string]*domain.LeaderboardResponse),
		buildingSnapshots:  make(map[string]*domain.LeaderboardResponse),
	}
}

// MutateUser applies fn to the stored user in place; intended for tests that
// need to simulate legacy account states (e.g. onboarding not completed).
func (m *MemoryStore) MutateUser(userID string, fn func(*domain.User)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return false
	}
	fn(u)
	return true
}

func (m *MemoryStore) Auth() store.AuthStore               { return m }
func (m *MemoryStore) Profile() store.ProfileStore         { return m }
func (m *MemoryStore) Privacy() store.PrivacyStore         { return m }
func (m *MemoryStore) Analytics() store.AnalyticsStore     { return m }
func (m *MemoryStore) Device() store.DeviceStore           { return m }
func (m *MemoryStore) Ingest() store.IngestStore           { return m }
func (m *MemoryStore) Export() store.ExportStore           { return m }
func (m *MemoryStore) Search() store.SearchStore           { return &memorySearchStore{m: m} }
func (m *MemoryStore) Leaderboard() store.LeaderboardStore { return m }
func (m *MemoryStore) Media() store.MediaStore             { return m }

// --- AuthStore Implementation ---

func (m *MemoryStore) CreateOrReplaceEmailChallenge(ctx context.Context, challenge domain.EmailChallenge, outbox domain.EmailOutbox) (*domain.EmailChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.emailChallenges {
		if c.ChallengeType == challenge.ChallengeType && c.EmailLookupHash == challenge.EmailLookupHash && c.ChallengeStatus == domain.ChallengeStatusPending {
			c.ChallengeStatus = domain.ChallengeStatusCancelled
		}
	}

	cCopy := challenge
	m.emailChallenges[challenge.ChallengeID] = &cCopy

	oCopy := outbox
	m.emailOutbox[outbox.EmailID] = &oCopy

	return &cCopy, nil
}

func (m *MemoryStore) FindPendingEmailChallenge(ctx context.Context, challengeType domain.ChallengeType, emailLookupHash [32]byte) (*domain.EmailChallenge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, c := range m.emailChallenges {
		if c.ChallengeType == challengeType && c.EmailLookupHash == emailLookupHash && c.ChallengeStatus == domain.ChallengeStatusPending {
			cCopy := *c
			return &cCopy, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *MemoryStore) UpdateEmailChallengeAttempts(ctx context.Context, challengeID string, attemptCount uint16, status domain.ChallengeStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.emailChallenges[challengeID]
	if !ok {
		return domain.ErrNotFound
	}
	c.AttemptCount = attemptCount
	c.ChallengeStatus = status
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *MemoryStore) RecordEmailChallengeFailure(ctx context.Context, challengeID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	challenge, ok := m.emailChallenges[challengeID]
	if !ok {
		return domain.ErrNotFound
	}
	if challenge.ChallengeStatus == domain.ChallengeStatusLocked {
		return domain.ErrChallengeLocked
	}
	if challenge.ChallengeStatus != domain.ChallengeStatusPending {
		return domain.ErrChallengeInvalid
	}
	if now.After(challenge.ExpiresAt) {
		challenge.ChallengeStatus = domain.ChallengeStatusExpired
		challenge.UpdatedAt = now
		return domain.ErrChallengeExpired
	}
	challenge.AttemptCount++
	challenge.UpdatedAt = now
	if challenge.AttemptCount >= challenge.MaxAttempts {
		challenge.ChallengeStatus = domain.ChallengeStatusLocked
		return domain.ErrChallengeLocked
	}
	return domain.ErrChallengeInvalid
}

func (m *MemoryStore) CompleteRegistrationTx(ctx context.Context, in store.RegistrationTxInput) (*domain.UserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Verify challenge exists and is pending
	ch, ok := m.emailChallenges[in.ChallengeID]
	if !ok || ch.ChallengeStatus != domain.ChallengeStatusPending {
		return nil, domain.ErrChallengeInvalid
	}
	if in.Session.CreatedAt.After(ch.ExpiresAt) {
		ch.ChallengeStatus = domain.ChallengeStatusExpired
		return nil, domain.ErrChallengeExpired
	}

	// Verify email uniqueness
	if in.User.EmailLookupHash != nil {
		for _, u := range m.users {
			if u.EmailLookupHash != nil && *u.EmailLookupHash == *in.User.EmailLookupHash {
				return nil, domain.ErrAccountExists
			}
		}
	}

	// Support fault injection for atomic rollback testing (USR-002)
	if m.FaultInjector != nil {
		if err := m.FaultInjector("CompleteRegistrationTx:credential_inserted"); err != nil {
			return nil, err
		}
	}

	// Atomically commit
	now := in.Session.CreatedAt
	ch.ChallengeStatus = domain.ChallengeStatusConsumed
	ch.ConsumedAt = &now
	ch.UserID = &in.User.UserID

	u := in.User
	m.users[u.UserID] = &u

	cred := in.Credential
	m.userCredentials[cred.UserID] = &cred

	priv := in.Privacy
	m.privacySettings[priv.UserID] = &priv

	sess := in.Session
	m.sessions[sess.SessionID] = &sess

	m.securityEvents = append(m.securityEvents, in.SecurityEvent)

	return &sess, nil
}

func (m *MemoryStore) FindUserByEmailHash(ctx context.Context, emailLookupHash [32]byte) (*domain.User, *domain.UserPasswordCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		if u.EmailLookupHash != nil && *u.EmailLookupHash == emailLookupHash {
			cred, ok := m.userCredentials[u.UserID]
			if !ok {
				return nil, nil, domain.ErrNotFound
			}
			uCopy := *u
			credCopy := *cred
			return &uCopy, &credCopy, nil
		}
	}
	return nil, nil, domain.ErrNotFound
}

func (m *MemoryStore) FindUserByID(ctx context.Context, userID string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	uCopy := *u
	return &uCopy, nil
}

func (m *MemoryStore) RecordLoginFailure(ctx context.Context, userID string, failedCount uint16, lockedUntil *time.Time, event domain.UserSecurityEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cred, ok := m.userCredentials[userID]; ok {
		cred.FailedLoginCount++
		now := time.Now().UTC()
		cred.LastFailedLoginAt = &now
		if cred.FailedLoginCount >= 10 {
			lockTime := now.Add(15 * time.Minute)
			cred.LockedUntil = &lockTime
		}
		cred.UpdatedAt = now
	}
	if event.EventID != "" {
		m.securityEvents = append(m.securityEvents, event)
	}
	return nil
}

func (m *MemoryStore) CreateSessionTx(ctx context.Context, session domain.UserSession, event domain.UserSecurityEvent) (*domain.UserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cred, ok := m.userCredentials[session.UserID]; ok {
		cred.FailedLoginCount = 0
		cred.LockedUntil = nil
		cred.UpdatedAt = time.Now().UTC()
	}

	var userSessions []*domain.UserSession
	for _, s := range m.sessions {
		if s.UserID == session.UserID && s.SessionStatus == domain.SessionStatusActive {
			userSessions = append(userSessions, s)
		}
	}
	if len(userSessions) >= 20 {
		sort.Slice(userSessions, func(i, j int) bool {
			return userSessions[i].LastSeenAt.Before(userSessions[j].LastSeenAt)
		})
		toRevoke := len(userSessions) - 19
		now := time.Now().UTC()
		reason := "session_limit"
		for i := 0; i < toRevoke; i++ {
			userSessions[i].SessionStatus = domain.SessionStatusRevoked
			userSessions[i].RevokedAt = &now
			userSessions[i].RevokeReason = &reason
		}
	}

	sCopy := session
	m.sessions[session.SessionID] = &sCopy
	m.securityEvents = append(m.securityEvents, event)

	return &sCopy, nil
}

func (m *MemoryStore) ResolveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (*domain.UserSession, *domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.sessions {
		if s.SessionTokenHash == tokenHash {
			if s.SessionStatus != domain.SessionStatusActive {
				return nil, nil, domain.ErrUnauthorized
			}
			if now.After(s.AbsoluteExpiresAt) || now.After(s.IdleExpiresAt) {
				return nil, nil, domain.ErrUnauthorized
			}

			u, ok := m.users[s.UserID]
			if !ok {
				return nil, nil, domain.ErrUnauthorized
			}

			cred, ok := m.userCredentials[s.UserID]
			if !ok || cred.CredentialVersion != s.CredentialVersion {
				return nil, nil, domain.ErrUnauthorized
			}

			sCopy := *s
			uCopy := *u
			return &sCopy, &uCopy, nil
		}
	}
	return nil, nil, domain.ErrUnauthorized
}

func (m *MemoryStore) RevokeSession(ctx context.Context, sessionID string, reason string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return domain.ErrNotFound
	}
	s.SessionStatus = domain.SessionStatusRevoked
	s.RevokedAt = &now
	s.RevokeReason = &reason
	s.UpdatedAt = now
	return nil
}

func (m *MemoryStore) RevokeUserSession(ctx context.Context, sessionID string, userID string, reason string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok || s.UserID != userID || s.SessionStatus != domain.SessionStatusActive {
		return domain.ErrNotFound
	}
	s.SessionStatus = domain.SessionStatusRevoked
	s.RevokedAt = &now
	s.RevokeReason = &reason
	s.UpdatedAt = now
	return nil
}

func (m *MemoryStore) RotateSessionCSRF(ctx context.Context, sessionID string, newCSRFHash [32]byte, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok || s.SessionStatus != domain.SessionStatusActive {
		return domain.ErrNotFound
	}
	s.CSRFTokenHash = newCSRFHash
	s.UpdatedAt = now
	return nil
}

func (m *MemoryStore) RevokeOtherSessions(ctx context.Context, userID, currentSessionID string, reason string, now time.Time, event domain.UserSecurityEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		if s.UserID == userID && s.SessionID != currentSessionID && s.SessionStatus == domain.SessionStatusActive {
			s.SessionStatus = domain.SessionStatusRevoked
			s.RevokedAt = &now
			s.RevokeReason = &reason
			s.UpdatedAt = now
		}
	}
	m.securityEvents = append(m.securityEvents, event)
	return nil
}

func (m *MemoryStore) ListUserSessions(ctx context.Context, userID string) ([]domain.UserSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.UserSession
	for _, s := range m.sessions {
		if s.UserID == userID {
			result = append(result, *s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) ResetPasswordTx(ctx context.Context, emailLookupHash [32]byte, codeHash [32]byte, newHash string, newVersion uint32, event domain.UserSecurityEvent, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var targetCh *domain.EmailChallenge
	for _, ch := range m.emailChallenges {
		if ch.EmailLookupHash == emailLookupHash && ch.ChallengeType == domain.ChallengeTypePasswordReset && ch.ChallengeStatus == domain.ChallengeStatusPending {
			targetCh = ch
			break
		}
	}
	if targetCh == nil || targetCh.UserID == nil {
		return domain.ErrNotFound
	}

	if now.After(targetCh.ExpiresAt) {
		targetCh.ChallengeStatus = domain.ChallengeStatusExpired
		targetCh.UpdatedAt = now
		return domain.ErrChallengeExpired
	}

	if subtle.ConstantTimeCompare(codeHash[:], targetCh.CodeHash[:]) != 1 {
		targetCh.AttemptCount++
		if targetCh.AttemptCount >= targetCh.MaxAttempts {
			targetCh.ChallengeStatus = domain.ChallengeStatusLocked
			targetCh.UpdatedAt = now
			return domain.ErrChallengeLocked
		}
		targetCh.UpdatedAt = now
		return domain.ErrChallengeInvalid
	}

	targetCh.ChallengeStatus = domain.ChallengeStatusConsumed
	targetCh.ConsumedAt = &now
	targetCh.UpdatedAt = now

	userID := *targetCh.UserID
	cred, ok := m.userCredentials[userID]
	if !ok {
		return domain.ErrNotFound
	}

	cred.PasswordHash = newHash
	cred.CredentialVersion = newVersion
	cred.PasswordChangedAt = now
	cred.FailedLoginCount = 0
	cred.LockedUntil = nil
	cred.UpdatedAt = now

	reason := "password_reset"
	for _, s := range m.sessions {
		if s.UserID == userID && s.SessionStatus == domain.SessionStatusActive {
			s.SessionStatus = domain.SessionStatusRevoked
			s.RevokedAt = &now
			s.RevokeReason = &reason
			s.UpdatedAt = now
		}
	}

	if event.EventID != "" {
		event.UserID = &userID
		m.securityEvents = append(m.securityEvents, event)
	}
	return nil
}

func (m *MemoryStore) TouchSessionLastSeen(ctx context.Context, sessionID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[sessionID]; ok {
		s.LastSeenAt = now
	}
	return nil
}

// --- ProfileStore Implementation ---

func (m *MemoryStore) GetUserProfile(ctx context.Context, userID string) (*domain.User, error) {
	return m.FindUserByID(ctx, userID)
}

func (m *MemoryStore) CompleteOnboardingTx(ctx context.Context, userID string, handle string, displayName string, timezone string, locale string, privacy domain.UserPrivacySettings, event domain.UserSecurityEvent, now time.Time) (*domain.User, *domain.UserPrivacySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, nil, domain.ErrNotFound
	}

	handle = strings.ToLower(strings.TrimSpace(handle))
	for id, other := range m.users {
		if id != userID && other.Handle != nil && *other.Handle == handle {
			return nil, nil, domain.ErrHandleTaken
		}
	}
	if hist, ok := m.handleHistory[handle]; ok && hist.UserID != userID && now.Before(hist.ReservedUntil) {
		return nil, nil, domain.ErrHandleTaken
	}

	u.Handle = &handle
	u.DisplayName = displayName
	u.TimezoneName = timezone
	u.Locale = locale
	u.OnboardingCompletedAt = &now
	u.ProfileVersion++
	u.UpdatedAt = now

	priv := privacy
	priv.UserID = userID
	priv.PrivacyVersion++
	priv.UpdatedAt = now
	m.privacySettings[userID] = &priv

	profileStatus := domain.ProfileStatusHidden
	if priv.PublicProfileEnabled {
		profileStatus = domain.ProfileStatusPublished
		u.LeaderboardVisibility = domain.LeaderboardVisibilityPublic
	} else {
		u.LeaderboardVisibility = domain.LeaderboardVisibilityPrivate
	}

	var bio *string
	if priv.ShowBio {
		bio = u.Bio
	}

	pubProf := domain.PublicUserProfile{
		UserID:               userID,
		Handle:               handle,
		DisplayName:          displayName,
		AvatarURL:            u.AvatarURL,
		Bio:                  bio,
		ProfileStatus:        profileStatus,
		ShowBio:              priv.ShowBio,
		ShowTokenTotal:       priv.ShowTokenTotal,
		ShowTrends:           priv.ShowTrends,
		ShowActivityCalendar: priv.ShowActivityCalendar,
		ShowAgentBreakdown:   priv.ShowAgentBreakdown,
		ShowSkillRanking:     priv.ShowSkillRanking,
		ShowAchievements:     priv.ShowAchievements,
		SourceProfileVersion: u.ProfileVersion,
		SourcePrivacyVersion: priv.PrivacyVersion,
		ProjectionVersion:    1,
		PublishedAt:          &now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	m.publicProfiles[userID] = &pubProf
	m.securityEvents = append(m.securityEvents, event)

	uCopy := *u
	pCopy := priv
	return &uCopy, &pCopy, nil
}

func (m *MemoryStore) UpdateProfileTx(ctx context.Context, userID string, displayName *string, handle *string, bio *string, timezone *string, locale *string, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}

	if expectedVersion > 0 && u.ProfileVersion != expectedVersion {
		return nil, domain.ErrPreconditionFailed
	}

	if handle != nil {
		newHandle := strings.ToLower(strings.TrimSpace(*handle))
		if u.Handle == nil || *u.Handle != newHandle {
			for id, other := range m.users {
				if id != userID && other.Handle != nil && *other.Handle == newHandle {
					return nil, domain.ErrHandleTaken
				}
			}
			if hist, ok := m.handleHistory[newHandle]; ok && hist.UserID != userID && now.Before(hist.ReservedUntil) {
				return nil, domain.ErrHandleTaken
			}

			if u.Handle != nil && *u.Handle != "" {
				m.handleHistory[*u.Handle] = &domain.UserHandleHistory{
					Handle:        *u.Handle,
					UserID:        userID,
					RedirectUntil: now.Add(7 * 24 * time.Hour),
					ReservedUntil: now.Add(30 * 24 * time.Hour),
					CreatedAt:     now,
				}
			}
			u.Handle = &newHandle
		}
	}

	if displayName != nil {
		u.DisplayName = *displayName
	}
	if bio != nil {
		u.Bio = bio
	}
	if timezone != nil {
		u.TimezoneName = *timezone
	}
	if locale != nil {
		u.Locale = *locale
	}

	u.ProfileVersion++
	u.UpdatedAt = now

	if pub, ok := m.publicProfiles[userID]; ok {
		if u.Handle != nil {
			pub.Handle = *u.Handle
		}
		pub.DisplayName = u.DisplayName
		pub.AvatarURL = u.AvatarURL
		if pub.ShowBio {
			pub.Bio = u.Bio
		} else {
			pub.Bio = nil
		}
		pub.SourceProfileVersion = u.ProfileVersion
		pub.ProjectionVersion++
		pub.UpdatedAt = now
	}

	m.securityEvents = append(m.securityEvents, event)
	uCopy := *u
	return &uCopy, nil
}

func (m *MemoryStore) IsHandleAvailable(ctx context.Context, handle string, excludeUserID string, now time.Time) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	handle = strings.ToLower(strings.TrimSpace(handle))
	for id, u := range m.users {
		if id != excludeUserID && u.Handle != nil && *u.Handle == handle {
			return false, nil
		}
	}
	if hist, ok := m.handleHistory[handle]; ok && hist.UserID != excludeUserID && now.Before(hist.ReservedUntil) {
		return false, nil
	}
	return true, nil
}

func (m *MemoryStore) GetRedirectHandle(ctx context.Context, oldHandle string, now time.Time) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	oldHandle = strings.ToLower(strings.TrimSpace(oldHandle))
	hist, ok := m.handleHistory[oldHandle]
	if !ok || now.After(hist.RedirectUntil) {
		return "", domain.ErrNotFound
	}
	u, ok := m.users[hist.UserID]
	if !ok || u.Handle == nil {
		return "", domain.ErrNotFound
	}
	return *u.Handle, nil
}

// --- PrivacyStore Implementation ---

func (m *MemoryStore) GetPrivacy(ctx context.Context, userID string) (*domain.UserPrivacySettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.privacySettings[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	pCopy := *p
	if u, ok := m.users[userID]; ok {
		pCopy.LeaderboardVisibility = u.LeaderboardVisibility
	}
	return &pCopy, nil
}

func (m *MemoryStore) UpdatePrivacyTx(ctx context.Context, userID string, in domain.UserPrivacySettings, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.UserPrivacySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.privacySettings[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}

	if expectedVersion > 0 && p.PrivacyVersion != expectedVersion {
		return nil, domain.ErrPreconditionFailed
	}

	u, ok := m.users[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}

	p.PublicProfileEnabled = in.PublicProfileEnabled
	p.LeaderboardVisibility = in.LeaderboardVisibility
	p.ShowBio = in.ShowBio
	p.ShowTokenTotal = in.ShowTokenTotal
	p.ShowTrends = in.ShowTrends
	p.ShowActivityCalendar = in.ShowActivityCalendar
	p.ShowAgentBreakdown = in.ShowAgentBreakdown
	p.ShowSkillRanking = in.ShowSkillRanking
	p.ShowAchievements = in.ShowAchievements
	p.PrivacyVersion++
	p.UpdatedAt = now

	visibility := in.LeaderboardVisibility
	if visibility == "" {
		visibility = domain.LeaderboardVisibilityPrivate
		if in.PublicProfileEnabled {
			visibility = domain.LeaderboardVisibilityPublic
		}
	}
	u.LeaderboardVisibility = visibility
	p.LeaderboardVisibility = visibility
	u.PublicProfileUpdatedAt = &now
	u.UpdatedAt = now

	pub, ok := m.publicProfiles[userID]
	if !ok && u.Handle != nil {
		pub = &domain.PublicUserProfile{
			UserID:      userID,
			Handle:      *u.Handle,
			DisplayName: u.DisplayName,
			AvatarURL:   u.AvatarURL,
			CreatedAt:   now,
		}
		m.publicProfiles[userID] = pub
	}
	if pub != nil {
		if in.PublicProfileEnabled && u.AccountStatus == domain.AccountStatusActive && u.OnboardingCompletedAt != nil {
			pub.ProfileStatus = domain.ProfileStatusPublished
			pub.PublishedAt = &now
		} else {
			pub.ProfileStatus = domain.ProfileStatusHidden
		}
		pub.ShowBio = in.ShowBio
		if in.ShowBio {
			pub.Bio = u.Bio
		} else {
			pub.Bio = nil
		}
		pub.ShowTokenTotal = in.ShowTokenTotal
		pub.ShowTrends = in.ShowTrends
		pub.ShowActivityCalendar = in.ShowActivityCalendar
		pub.ShowAgentBreakdown = in.ShowAgentBreakdown
		pub.ShowSkillRanking = in.ShowSkillRanking
		pub.ShowAchievements = in.ShowAchievements
		pub.SourcePrivacyVersion = p.PrivacyVersion
		pub.SourceProfileVersion = u.ProfileVersion
		pub.ProjectionVersion++
		pub.UpdatedAt = now
	}

	m.securityEvents = append(m.securityEvents, event)
	pCopy := *p
	return &pCopy, nil
}

func (m *MemoryStore) GetPublicProfileByHandle(ctx context.Context, handle string, now time.Time) (*domain.PublicUserProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	handle = strings.ToLower(strings.TrimSpace(handle))
	for _, pub := range m.publicProfiles {
		if strings.EqualFold(pub.Handle, handle) {
			if pub.ProfileStatus != domain.ProfileStatusPublished {
				return nil, domain.ErrNotFound
			}
			u, ok := m.users[pub.UserID]
			if !ok || u.AccountStatus != domain.AccountStatusActive {
				return nil, domain.ErrNotFound
			}
			priv, ok := m.privacySettings[pub.UserID]
			if !ok || !priv.PublicProfileEnabled {
				return nil, domain.ErrNotFound
			}
			pubCopy := *pub
			return &pubCopy, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *MemoryStore) SetAccountStatusTx(ctx context.Context, userID string, status domain.AccountStatus, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if status != domain.AccountStatusActive && status != domain.AccountStatusSuspended {
		return domain.ErrInvalidArgument
	}
	u, ok := m.users[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if u.AccountStatus == status {
		return nil
	}
	if !((u.AccountStatus == domain.AccountStatusActive && status == domain.AccountStatusSuspended) ||
		(u.AccountStatus == domain.AccountStatusSuspended && status == domain.AccountStatusActive)) {
		return domain.ErrConflict
	}
	u.AccountStatus = status
	u.UpdatedAt = now
	if pub := m.publicProfiles[userID]; pub != nil {
		privacy := m.privacySettings[userID]
		if status == domain.AccountStatusActive && privacy != nil && privacy.PublicProfileEnabled && u.OnboardingCompletedAt != nil {
			pub.ProfileStatus = domain.ProfileStatusPublished
			pub.PublishedAt = &now
		} else {
			pub.ProfileStatus = domain.ProfileStatusHidden
			pub.PublishedAt = nil
		}
		pub.ProjectionVersion++
		pub.UpdatedAt = now
	}
	return nil
}

func (m *MemoryStore) RequestDeletionTx(ctx context.Context, req domain.DataDeletionRequest, event domain.UserSecurityEvent, now time.Time) (*domain.DataDeletionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rCopy := req
	m.deletionRequests[req.RequestID] = &rCopy

	if req.UserID != nil {
		if req.DeletionScope == "account" {
			if u, ok := m.users[*req.UserID]; ok {
				u.AccountStatus = domain.AccountStatusDeletionPending
				u.LeaderboardVisibility = domain.LeaderboardVisibilityPrivate
				u.UpdatedAt = now
			}
			if priv, ok := m.privacySettings[*req.UserID]; ok {
				priv.PublicProfileEnabled = false
				priv.UpdatedAt = now
			}
			if pub, ok := m.publicProfiles[*req.UserID]; ok {
				pub.ProfileStatus = domain.ProfileStatusHidden
				pub.ProjectionVersion++
				pub.UpdatedAt = now
			}
			for _, b := range m.bindingChallenges {
				if b.UserID == *req.UserID && b.ChallengeStatus == domain.ChallengeStatusPending {
					b.ChallengeStatus = domain.ChallengeStatusCancelled
					b.UpdatedAt = now
				}
			}
		}
	}

	m.securityEvents = append(m.securityEvents, event)
	return &rCopy, nil
}

func (m *MemoryStore) CancelDeletionTx(ctx context.Context, requestID string, userID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.deletionRequests[requestID]
	if !ok || req.UserID == nil || *req.UserID != userID {
		return domain.ErrNotFound
	}
	if req.RequestStatus != domain.DeletionStatusPending {
		return domain.ErrConflict
	}
	if req.CancelBefore != nil && now.After(*req.CancelBefore) {
		return domain.ErrConflict
	}

	req.RequestStatus = domain.DeletionStatusCancelled
	req.CancelledAt = &now
	req.Phase = "cancelled"

	if req.DeletionScope == "account" {
		if u, ok := m.users[userID]; ok {
			u.AccountStatus = domain.AccountStatusActive
			u.LeaderboardVisibility = domain.LeaderboardVisibilityPrivate
			u.UpdatedAt = now
		}
		if priv, ok := m.privacySettings[userID]; ok {
			priv.PublicProfileEnabled = false
			priv.UpdatedAt = now
		}
		if pub, ok := m.publicProfiles[userID]; ok {
			pub.ProfileStatus = domain.ProfileStatusHidden
			pub.ProjectionVersion++
			pub.UpdatedAt = now
		}
	}
	return nil
}

func (m *MemoryStore) GetDeletionRequest(ctx context.Context, requestID string, userID string) (*domain.DataDeletionRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	req, ok := m.deletionRequests[requestID]
	if !ok || req.UserID == nil || *req.UserID != userID {
		return nil, domain.ErrNotFound
	}
	reqCopy := *req
	return &reqCopy, nil
}

func (m *MemoryStore) ClaimPendingDeletion(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*domain.DataDeletionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, req := range m.deletionRequests {
		if req.RequestStatus == domain.DeletionStatusPending {
			if req.CancelBefore == nil || now.After(*req.CancelBefore) {
				req.RequestStatus = domain.DeletionStatusRunning
				req.Phase = "running"
				rCopy := *req
				return &rCopy, nil
			}
		} else if req.RequestStatus == domain.DeletionStatusRunning {
			if req.CancelBefore != nil && now.After(req.RequestedAt.Add(leaseDuration)) {
				rCopy := *req
				return &rCopy, nil
			}
		}
	}
	return nil, domain.ErrNotFound
}

func (m *MemoryStore) ExecuteDeletionPhase(ctx context.Context, requestID string, workerID string, phase string, cursor uint64, auditRef string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.deletionRequests[requestID]
	if !ok {
		return domain.ErrNotFound
	}

	req.Phase = phase
	req.ProgressCursor = cursor

	if req.UserID != nil {
		userID := *req.UserID
		switch phase {
		case "phase1_revoke_sessions_devices":
			for _, inst := range m.installations {
				if inst.UserID == userID {
					inst.InstallationStatus = domain.InstallationStatusRevoked
					inst.RevokedAt = &now
					inst.StatusVersion++
					inst.UpdatedAt = now
				}
			}
			for _, sess := range m.sessions {
				if sess.UserID == userID {
					reason := "account_deleted"
					sess.SessionStatus = domain.SessionStatusRevoked
					sess.RevokedAt = &now
					sess.RevokeReason = &reason
					sess.UpdatedAt = now
				}
			}
		case "phase2_delete_credentials_outbox_exports":
			delete(m.userCredentials, userID)
			for id, out := range m.emailOutbox {
				if out.UserID != nil && *out.UserID == userID {
					delete(m.emailOutbox, id)
				}
			}
			for id, ch := range m.emailChallenges {
				if ch.UserID != nil && *ch.UserID == userID {
					delete(m.emailChallenges, id)
				}
			}
			for id, exp := range m.exportJobs {
				if exp.UserID == userID {
					delete(m.exportJobs, id)
				}
			}
		case "phase3_delete_upload_objects":
			for id, obj := range m.uploadObjects {
				if obj.UserID == userID {
					obj.UploadStatus = domain.UploadStatusDeleted
					obj.DeletedAt = &now
					delete(m.uploadObjects, id)
				}
			}
		case "phase4_delete_usage_events":
			delete(m.userMetricFixtures, userID)
			delete(m.userSkillFixtures, userID)
			delete(m.userTrendFixtures, userID)
		case "phase5_tombstone_user", "completed":
			if u, ok := m.users[userID]; ok {
				u.EmailLookupHash = nil
				u.EmailCiphertext = nil
				u.Handle = nil
				u.AvatarURL = nil
				u.AvatarObjectID = nil
				u.Bio = nil
				u.DisplayName = "Deleted user"
				u.AccountStatus = domain.AccountStatusDeleted
				u.LeaderboardVisibility = domain.LeaderboardVisibilityPrivate
				u.DeletedAt = &now
				u.UpdatedAt = now
			}
			delete(m.publicProfiles, userID)
			delete(m.privacySettings, userID)
			req.RequestStatus = domain.DeletionStatusCompleted
			req.Phase = "completed"
			req.CompletedAt = &now
			req.AuditReference = &auditRef
		}
	}

	return nil
}

// --- AnalyticsStore Implementation ---

func (m *MemoryStore) GetPersonalSummary(ctx context.Context, userID string, r domain.TimeRange) (*domain.PersonalSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}

	now := time.Now().UTC()
	var rank *int
	var delta *int
	var percentile *float64
	if u.LeaderboardVisibility == domain.LeaderboardVisibilityPublic {
		rVal := 42
		dVal := 3
		pVal := 98.5
		rank = &rVal
		delta = &dVal
		percentile = &pVal
	}

	fix, hasFixture := m.userMetricFixtures[userID]
	if !hasFixture {
		// No collected data for this user yet: return an honest empty summary
		// instead of demo numbers.
		return &domain.PersonalSummary{
			Range: r,
			Metrics: domain.PersonalSummaryMetrics{
				EstimatedCost:      domain.MetricCost{Supported: false},
				TotalTokens:        domain.MetricBigInt{Supported: false},
				GeneratedCodeLines: domain.MetricBigInt{Supported: false},
				TokensPerCodeLine:  domain.MetricDecimal{Supported: false},
				InputContextTokens: domain.MetricBigInt{Supported: false},
				OutputTokens:       domain.MetricBigInt{Supported: false},
				CacheHitRate:       domain.MetricDecimal{Supported: false},
				ActiveDurationMs:   domain.MetricBigInt{Supported: false},
				MessageCount:       domain.MetricBigInt{Supported: false},
				UserMessageCount:   domain.MetricBigInt{Supported: false},
			},
			Ranking: domain.PersonalSummaryRanking{
				Visibility: u.LeaderboardVisibility,
			},
			Sync: domain.PersonalSummarySync{},
		}, nil
	}

	totalTokensVal := fix.ExactTokenTotal + fix.DerivedTokenTotal
	totalTokensStr := fmt.Sprintf("%d", totalTokensVal)
	codeLinesStr := fmt.Sprintf("%d", fix.CodeGeneratedLines)

	var tokensPerCodeLineStr *string
	if fix.CodeGeneratedLines > 0 {
		str := fmt.Sprintf("%.2f", float64(totalTokensVal)/float64(fix.CodeGeneratedLines))
		tokensPerCodeLineStr = &str
	}

	aggVer := fix.AggregationVersion
	if aggVer == 0 {
		aggVer = 2
	}

	var inputTokensStr, outputTokensStr, activeDurationStr, messageCountStr, userMessageCountStr *string
	var cacheHitRateStr *string
	extSupported := fix.ExtensionsSupported && aggVer >= 2

	if extSupported {
		inpVal := fix.TokenInputTotal + fix.TokenCacheReadTotal
		inpStr := fmt.Sprintf("%d", inpVal)
		inputTokensStr = &inpStr

		outStr := fmt.Sprintf("%d", fix.TokenOutputTotal)
		outputTokensStr = &outStr

		denom := fix.TokenInputTotal + fix.TokenCacheReadTotal
		if denom > 0 {
			rateStr := fmt.Sprintf("%.3f", float64(fix.TokenCacheReadTotal)/float64(denom))
			cacheHitRateStr = &rateStr
		}

		durStr := fmt.Sprintf("%d", fix.ActiveDurationMs)
		activeDurationStr = &durStr

		msgStr := fmt.Sprintf("%d", fix.MessageCount)
		messageCountStr = &msgStr

		usrMsgStr := fmt.Sprintf("%d", fix.UserMessageCount)
		userMessageCountStr = &usrMsgStr
	}

	return &domain.PersonalSummary{
		Range: r,
		Metrics: domain.PersonalSummaryMetrics{
			EstimatedCost:      domain.MetricCost{Amount: fix.CostAmount, Currency: fix.CostCurrency, Supported: fix.CostSupported},
			TotalTokens:        domain.MetricBigInt{Value: &totalTokensStr, Supported: true},
			GeneratedCodeLines: domain.MetricBigInt{Value: &codeLinesStr, Supported: true},
			TokensPerCodeLine:  domain.MetricDecimal{Value: tokensPerCodeLineStr, Supported: true},
			InputContextTokens: domain.MetricBigInt{Value: inputTokensStr, Supported: extSupported},
			OutputTokens:       domain.MetricBigInt{Value: outputTokensStr, Supported: extSupported},
			CacheHitRate:       domain.MetricDecimal{Value: cacheHitRateStr, Supported: extSupported},
			ActiveDurationMs:   domain.MetricBigInt{Value: activeDurationStr, Supported: extSupported},
			MessageCount:       domain.MetricBigInt{Value: messageCountStr, Supported: extSupported},
			UserMessageCount:   domain.MetricBigInt{Value: userMessageCountStr, Supported: extSupported},
		},
		Ranking: domain.PersonalSummaryRanking{
			Visibility: u.LeaderboardVisibility,
			Rank:       rank,
			Delta:      delta,
			Percentile: percentile,
		},
		Sync: domain.PersonalSummarySync{
			LastCommittedAt:   &now,
			PendingLocalCount: nil,
		},
		DataWatermarkAt:    &now,
		AggregationVersion: aggVer,
	}, nil
}

func (m *MemoryStore) GetTokenTrend(ctx context.Context, userID string, r domain.TimeRange, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().UTC()
	fixtures, hasFixtures := m.userTrendFixtures[userID]

	if !hasFixtures {
		// No collected data yet: empty trend.
		return &domain.TrendResponse{
			Range:       r,
			Mode:        mode,
			AgentID:     agentID,
			ProviderID:  providerID,
			ModelID:     modelID,
			Granularity: "day",
			Points:      []domain.TrendPoint{},
		}, nil
	}

	type dayBucket struct {
		Date             string
		ExactTokens      uint64
		DerivedTokens    uint64
		InputTokens      uint64
		OutputTokens     uint64
		CacheReadTokens  uint64
		CacheWriteTokens uint64
		ReasoningTokens  uint64
	}

	buckets := make(map[string]*dayBucket)
	for _, f := range fixtures {
		if agentID != nil && *agentID != "all" && f.AgentID != *agentID {
			continue
		}
		if providerID != nil && *providerID != "all" && f.ProviderID != *providerID {
			continue
		}
		if modelID != nil && *modelID != "all" && f.ModelID != *modelID {
			continue
		}

		b, ok := buckets[f.Date]
		if !ok {
			b = &dayBucket{Date: f.Date}
			buckets[f.Date] = b
		}
		b.ExactTokens += f.ExactTokens
		b.DerivedTokens += f.DerivedTokens
		b.InputTokens += f.InputTokens
		b.OutputTokens += f.OutputTokens
		b.CacheReadTokens += f.CacheReadTokens
		b.CacheWriteTokens += f.CacheWriteTokens
		b.ReasoningTokens += f.ReasoningTokens
	}

	var dates []string
	for d := range buckets {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	var points []domain.TrendPoint
	for _, d := range dates {
		b := buckets[d]
		tot := fmt.Sprintf("%d", b.ExactTokens+b.DerivedTokens)
		inp := fmt.Sprintf("%d", b.InputTokens)
		out := fmt.Sprintf("%d", b.OutputTokens)
		cr := fmt.Sprintf("%d", b.CacheReadTokens)
		cw := fmt.Sprintf("%d", b.CacheWriteTokens)
		rsn := fmt.Sprintf("%d", b.ReasoningTokens)

		if mode == "structure" {
			points = append(points, domain.TrendPoint{
				Date:             d,
				InputTokens:      &inp,
				OutputTokens:     &out,
				CacheReadTokens:  &cr,
				CacheWriteTokens: &cw,
				ReasoningTokens:  &rsn,
			})
		} else {
			points = append(points, domain.TrendPoint{
				Date:       d,
				TokenTotal: &tot,
			})
		}
	}

	return &domain.TrendResponse{
		Range:              r,
		Mode:               mode,
		AgentID:            agentID,
		ProviderID:         providerID,
		ModelID:            modelID,
		Granularity:        "day",
		Points:             points,
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (m *MemoryStore) GetAgentBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	return &domain.BreakdownResponse{Range: r, Items: []domain.BreakdownItem{}}, nil
}

func (m *MemoryStore) GetModelBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	return &domain.BreakdownResponse{Range: r, Items: []domain.BreakdownItem{}}, nil
}

func (m *MemoryStore) GetSkillRanking(ctx context.Context, userID string, r domain.TimeRange) (*domain.SkillsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().UTC()
	fixtures, hasFixtures := m.userSkillFixtures[userID]

	if !hasFixtures {
		return &domain.SkillsResponse{Range: r, Skills: []domain.SkillItem{}}, nil
	}

	type sortableSkill struct {
		item  domain.SkillItem
		count uint64
	}
	var sList []sortableSkill
	for _, f := range fixtures {
		pubName := f.SkillPublicName
		skillID := f.SkillID
		if pubName == "" {
			pubName = "Private Skill"
			hash := sha256.Sum256([]byte(f.SkillKey))
			skillID = fmt.Sprintf("skl_%x", hash[:4])
		}
		sList = append(sList, sortableSkill{
			item: domain.SkillItem{
				SkillID:          skillID,
				SkillPublicName:  pubName,
				UseCount:         fmt.Sprintf("%d", f.UseCount),
				ActiveDays:       f.ActiveDays,
				SuccessRate:      f.SuccessRate,
				PreviousDeltaPct: f.PreviousDeltaPct,
			},
			count: f.UseCount,
		})
	}

	sort.Slice(sList, func(i, j int) bool {
		return sList[i].count > sList[j].count
	})

	var items []domain.SkillItem
	for _, s := range sList {
		items = append(items, s.item)
	}

	return &domain.SkillsResponse{
		Range:              r,
		Skills:             items,
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (m *MemoryStore) GetActivityCalendar(ctx context.Context, userID string, r domain.TimeRange) (*domain.CalendarResponse, error) {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		loc = time.UTC
	}
	from, to := r.From.In(loc), r.To.In(loc)
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	// No collected data yet: render an all-zero calendar.
	var days []domain.CalendarDay
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		days = append(days, domain.CalendarDay{Date: day.Format("2006-01-02"), Active: false, Level: 0, TokenTotal: "0"})
	}
	return &domain.CalendarResponse{Days: days}, nil
}

func (m *MemoryStore) GetActivity(ctx context.Context, userID string, q domain.ActivityQuery) ([]domain.ActivityRow, error) {
	// No collected data yet: empty activity list.
	return []domain.ActivityRow{}, nil
}

func (m *MemoryStore) GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error) {
	return &domain.FilterOptions{
		Agents:    []string{},
		Providers: []string{},
		Models:    []string{},
	}, nil
}

// --- DeviceStore Implementation ---

func (m *MemoryStore) ListInstallations(ctx context.Context, userID string) ([]domain.Installation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.Installation
	for _, inst := range m.installations {
		if inst.UserID == userID {
			result = append(result, *inst)
		}
	}
	return result, nil
}

func (m *MemoryStore) GetInstallation(ctx context.Context, installationID string, userID string) (*domain.Installation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.installations[installationID]
	if !ok || inst.UserID != userID {
		return nil, domain.ErrNotFound
	}
	instCopy := *inst
	return &instCopy, nil
}

func (m *MemoryStore) CreateBindingChallenge(ctx context.Context, challenge domain.DeviceBindingChallenge) (*domain.DeviceBindingChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.bindingChallenges {
		if c.SessionID == challenge.SessionID && c.ChallengeStatus == domain.ChallengeStatusPending {
			c.ChallengeStatus = domain.ChallengeStatusCancelled
			c.ActiveSessionKey = nil
		}
	}

	cCopy := challenge
	m.bindingChallenges[challenge.ChallengeID] = &cCopy
	return &cCopy, nil
}

func (m *MemoryStore) CancelBindingChallenge(ctx context.Context, challengeID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.bindingChallenges[challengeID]
	if !ok || c.UserID != userID {
		return domain.ErrNotFound
	}
	c.ChallengeStatus = domain.ChallengeStatusCancelled
	c.ActiveSessionKey = nil
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *MemoryStore) ClaimInstallationTx(ctx context.Context, codeHash [32]byte, inst domain.Installation, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var challenge *domain.DeviceBindingChallenge
	for _, c := range m.bindingChallenges {
		if c.CodeLookupHash == codeHash {
			if c.ChallengeStatus == domain.ChallengeStatusPending {
				challenge = c
				break
			}
			if c.ChallengeStatus == domain.ChallengeStatusConsumed && c.ConsumedInstallationID != nil {
				if existing, ok := m.installations[*c.ConsumedInstallationID]; ok {
					if existing.DevicePublicKey == inst.DevicePublicKey {
						eCopy := *existing
						return &eCopy, nil
					}
				}
			}
		}
	}
	if challenge == nil {
		return nil, domain.ErrChallengeInvalid
	}
	if now.After(challenge.ExpiresAt) {
		challenge.ChallengeStatus = domain.ChallengeStatusExpired
		challenge.ActiveSessionKey = nil
		return nil, domain.ErrChallengeExpired
	}

	sess, ok := m.sessions[challenge.SessionID]
	if !ok || sess.SessionStatus != domain.SessionStatusActive || now.After(sess.AbsoluteExpiresAt) || now.After(sess.IdleExpiresAt) {
		return nil, domain.ErrChallengeInvalid
	}

	u, ok := m.users[challenge.UserID]
	if !ok || u.AccountStatus != domain.AccountStatusActive || u.OnboardingCompletedAt == nil {
		return nil, domain.ErrForbidden
	}

	for _, existing := range m.installations {
		if existing.DevicePublicKey == inst.DevicePublicKey {
			if existing.UserID == challenge.UserID && existing.InstallationStatus == domain.InstallationStatusActive {
				challenge.ChallengeStatus = domain.ChallengeStatusConsumed
				challenge.ConsumedInstallationID = &existing.InstallationID
				challenge.ConsumedAt = &now
				challenge.ActiveSessionKey = nil
				eCopy := *existing
				return &eCopy, nil
			}
			return nil, domain.ErrPublicKeyConflict
		}
	}

	instCopy := inst
	instCopy.UserID = challenge.UserID
	m.installations[inst.InstallationID] = &instCopy

	challenge.ChallengeStatus = domain.ChallengeStatusConsumed
	challenge.ConsumedInstallationID = &inst.InstallationID
	challenge.ConsumedAt = &now
	challenge.ActiveSessionKey = nil

	return &instCopy, nil
}

func (m *MemoryStore) RegisterInstallationTx(ctx context.Context, inst domain.Installation, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[inst.UserID]
	if !ok || u.AccountStatus != domain.AccountStatusActive {
		return nil, domain.ErrForbidden
	}

	for _, existing := range m.installations {
		if existing.DevicePublicKey == inst.DevicePublicKey {
			if existing.UserID == inst.UserID && existing.InstallationStatus == domain.InstallationStatusActive {
				eCopy := *existing
				return &eCopy, nil
			}
			return nil, domain.ErrPublicKeyConflict
		}
	}

	instCopy := inst
	m.installations[inst.InstallationID] = &instCopy
	return &instCopy, nil
}

func (m *MemoryStore) UpdateInstallationName(ctx context.Context, installationID, userID string, name string, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.installations[installationID]
	if !ok || inst.UserID != userID {
		return nil, domain.ErrNotFound
	}
	inst.DeviceName = &name
	inst.UpdatedAt = now
	instCopy := *inst
	return &instCopy, nil
}

func (m *MemoryStore) PauseInstallation(ctx context.Context, installationID, userID string, reason string, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.installations[installationID]
	if !ok || inst.UserID != userID {
		return nil, domain.ErrNotFound
	}
	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusDisabled {
		instCopy := *inst
		return &instCopy, nil
	}

	inst.InstallationStatus = domain.InstallationStatusDisabled
	inst.DisabledAt = &now
	inst.DisabledReason = &reason
	inst.StatusVersion++
	inst.UpdatedAt = now
	instCopy := *inst
	return &instCopy, nil
}

func (m *MemoryStore) ResumeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.installations[installationID]
	if !ok || inst.UserID != userID {
		return nil, domain.ErrNotFound
	}
	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusActive {
		instCopy := *inst
		return &instCopy, nil
	}
	if inst.DisabledReason != nil && *inst.DisabledReason != "user_paused" {
		return nil, domain.ErrForbidden
	}

	inst.InstallationStatus = domain.InstallationStatusActive
	inst.DisabledAt = nil
	inst.DisabledReason = nil
	inst.StatusVersion++
	inst.UpdatedAt = now
	instCopy := *inst
	return &instCopy, nil
}

func (m *MemoryStore) RevokeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.installations[installationID]
	if !ok || inst.UserID != userID {
		return nil, domain.ErrNotFound
	}
	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		instCopy := *inst
		return &instCopy, nil
	}

	inst.InstallationStatus = domain.InstallationStatusRevoked
	inst.RevokedAt = &now
	inst.StatusVersion++
	inst.UpdatedAt = now
	instCopy := *inst
	return &instCopy, nil
}

func (m *MemoryStore) AuthorizeIngest(ctx context.Context, installationID string) (*domain.Installation, *domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.installations[installationID]
	if !ok {
		return nil, nil, domain.ErrNotFound
	}
	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusDisabled {
		return nil, nil, domain.ErrDeviceDisabled
	}

	u, ok := m.users[inst.UserID]
	if !ok || u.AccountStatus != domain.AccountStatusActive {
		return nil, nil, domain.ErrAccountSuspended
	}

	instCopy := *inst
	uCopy := *u
	return &instCopy, &uCopy, nil
}

func (m *MemoryStore) GetIngestInstallation(ctx context.Context, installationID string) (*domain.Installation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.installations[installationID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copy := *inst
	return &copy, nil
}

func (m *MemoryStore) CommitIngest(ctx context.Context, batch domain.IngestBatch) (*domain.IngestResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.installations[batch.InstallationID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusDisabled {
		return nil, domain.ErrDeviceDisabled
	}
	user, ok := m.users[inst.UserID]
	if !ok || user.AccountStatus != domain.AccountStatusActive {
		return nil, domain.ErrAccountSuspended
	}

	nonceKey := batch.InstallationID + ":" + string(batch.NonceHash[:])
	if _, exists := m.ingestNonces[nonceKey]; exists {
		return nil, domain.ErrNonceReplay
	}
	m.ingestNonces[nonceKey] = batch.NonceExpiresAt

	batchKey := batch.InstallationID + ":" + batch.BatchID
	if existing, exists := m.ingestBatches[batchKey]; exists {
		if m.ingestBatchHashes[batchKey] != batch.RequestSHA256 {
			delete(m.ingestNonces, nonceKey)
			return nil, domain.ErrBatchHashConflict
		}
		copy := *existing
		return &copy, nil
	}
	var accepted, duplicates uint32
	for _, event := range batch.Events {
		eventKey := batch.InstallationID + ":" + string(event.EventID[:])
		if _, exists := m.usageEvents[eventKey]; exists {
			duplicates++
			continue
		}
		m.usageEvents[eventKey] = event
		accepted++
	}

	result := &domain.IngestResult{
		BatchID:        batch.BatchID,
		AcceptedCount:  accepted,
		DuplicateCount: duplicates,
		RejectedCount:  batch.RejectedCount,
		CommittedAt:    batch.ReceivedAt,
	}
	m.ingestBatches[batchKey] = result
	m.ingestBatchHashes[batchKey] = batch.RequestSHA256
	inst.LastSeenAt = &batch.ReceivedAt
	copy := *result
	return &copy, nil
}

// --- ExportStore Implementation ---

func (m *MemoryStore) CreateJob(ctx context.Context, job domain.DataExportJob, idempotencyKeys []string) (*domain.DataExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidateKeys := make(map[string]struct{}, len(idempotencyKeys))
	for _, key := range idempotencyKeys {
		candidateKeys[key] = struct{}{}
	}
	for _, j := range m.exportJobs {
		_, matches := candidateKeys[j.IdempotencyKey]
		if j.UserID == job.UserID && matches {
			if j.RequestHash == job.RequestHash {
				jCopy := *j
				return &jCopy, nil
			}
			return nil, domain.ErrIdempotencyReused
		}
	}

	jCopy := job
	m.exportJobs[job.ExportID] = &jCopy
	return &jCopy, nil
}

func (m *MemoryStore) ListJobs(ctx context.Context, userID string) ([]domain.DataExportJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.DataExportJob
	for _, j := range m.exportJobs {
		if j.UserID == userID {
			result = append(result, *j)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) GetJob(ctx context.Context, exportID, userID string) (*domain.DataExportJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	j, ok := m.exportJobs[exportID]
	if !ok || j.UserID != userID {
		return nil, domain.ErrNotFound
	}
	jCopy := *j
	return &jCopy, nil
}

func (m *MemoryStore) ClaimPendingJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*domain.DataExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, j := range m.exportJobs {
		if j.JobStatus == domain.ExportJobStatusPending {
			j.JobStatus = domain.ExportJobStatusRunning
			j.LockedBy = &workerID
			j.LockedAt = &now
			j.StartedAt = &now
			jCopy := *j
			return &jCopy, nil
		} else if j.JobStatus == domain.ExportJobStatusRunning {
			if j.LockedAt != nil && now.After(j.LockedAt.Add(leaseDuration)) {
				j.LockedBy = &workerID
				j.LockedAt = &now
				jCopy := *j
				return &jCopy, nil
			}
		}
	}
	return nil, domain.ErrNotFound
}

func (m *MemoryStore) CompleteJob(ctx context.Context, exportID string, workerID string, objectKey string, fileSha256 [32]byte, fileSize uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.exportJobs[exportID]
	if !ok {
		return domain.ErrNotFound
	}
	j.JobStatus = domain.ExportJobStatusCompleted
	j.ObjectKey = &objectKey
	j.FileSha256 = &fileSha256
	j.FileSize = &fileSize
	j.CompletedAt = &now
	exp := now.Add(24 * time.Hour)
	j.ExpiresAt = &exp
	j.UpdatedAt = now
	return nil
}

func (m *MemoryStore) FailJob(ctx context.Context, exportID string, workerID string, lastError string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.exportJobs[exportID]
	if !ok {
		return domain.ErrNotFound
	}
	j.JobStatus = domain.ExportJobStatusFailed
	j.LastErrorCode = &lastError
	j.UpdatedAt = now
	return nil
}

// --- SearchStore Implementation ---

type memorySearchStore struct {
	m *MemoryStore
}

func (s *memorySearchStore) Search(ctx context.Context, query string, limit int, now time.Time) (*domain.SearchResponse, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	var users []domain.SearchUserResult

	for _, pub := range s.m.publicProfiles {
		if pub.ProfileStatus == domain.ProfileStatusPublished {
			u, ok := s.m.users[pub.UserID]
			if !ok || u.AccountStatus != domain.AccountStatusActive {
				continue
			}
			priv, ok := s.m.privacySettings[pub.UserID]
			if !ok || !priv.PublicProfileEnabled {
				continue
			}

			if strings.Contains(strings.ToLower(pub.Handle), query) || strings.Contains(strings.ToLower(pub.DisplayName), query) {
				rVal := 1
				users = append(users, domain.SearchUserResult{
					Handle:      pub.Handle,
					DisplayName: pub.DisplayName,
					AvatarURL:   pub.AvatarURL,
					Bio:         pub.Bio,
					Rank:        &rVal,
				})
			}
		}
	}

	agentCatalog := []domain.SearchAgentResult{
		{AgentID: "claude-code", Name: "Claude Code", Description: "Anthropic's terminal coding agent"},
		{AgentID: "cursor", Name: "Cursor", Description: "AI-first code editor"},
		{AgentID: "codex", Name: "Codex CLI", Description: "OpenAI terminal agent"},
	}

	var agents []domain.SearchAgentResult
	for _, a := range agentCatalog {
		if strings.Contains(strings.ToLower(a.AgentID), query) || strings.Contains(strings.ToLower(a.Name), query) {
			agents = append(agents, a)
		}
	}

	// Skills Search with minimum sample requirement (USR-106): min 5 public users & 3 active days
	var skills []domain.SearchSkillResult
	seenSkills := make(map[string]bool)

	for uid, skillList := range s.m.userSkillFixtures {
		u, uOk := s.m.users[uid]
		priv, pOk := s.m.privacySettings[uid]
		if !uOk || !pOk || u.AccountStatus != domain.AccountStatusActive || !priv.PublicProfileEnabled {
			continue
		}

		for _, sk := range skillList {
			if sk.SkillPublicName == "" || seenSkills[sk.SkillID] {
				continue
			}
			if sk.PublicUserCount >= 5 && sk.ActiveDays >= 3 {
				if strings.Contains(strings.ToLower(sk.SkillPublicName), query) || strings.Contains(strings.ToLower(sk.SkillID), query) {
					skills = append(skills, domain.SearchSkillResult{
						SkillID:         sk.SkillID,
						SkillPublicName: sk.SkillPublicName,
						UseCount:        fmt.Sprintf("%d", sk.UseCount),
						PublicUserCount: sk.PublicUserCount,
						ActiveDays:      sk.ActiveDays,
					})
					seenSkills[sk.SkillID] = true
				}
			}
		}
	}

	return &domain.SearchResponse{
		Users:  users,
		Agents: agents,
		Skills: skills,
	}, nil
}

// --- LeaderboardStore Implementation ---

func (m *MemoryStore) PublishSnapshot(ctx context.Context, snapshotID string, boardKey, window, metric string, entries []domain.LeaderboardEntry, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	resp := &domain.LeaderboardResponse{
		SnapshotID:      snapshotID,
		BoardKey:        boardKey,
		Window:          window,
		Metric:          metric,
		Entries:         entries,
		DataWatermarkAt: &now,
	}
	m.publishedSnapshots[snapshotID] = resp
	m.publishedSnapshots[boardKey+":"+window+":"+metric] = resp
	return nil
}

func (m *MemoryStore) GetLeaderboard(ctx context.Context, boardKey, window, metric string, cursor *string, limit int) (*domain.LeaderboardResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := boardKey + ":" + window + ":" + metric
	if snap, ok := m.publishedSnapshots[key]; ok {
		var filteredEntries []domain.LeaderboardEntry
		rank := 1
		for _, e := range snap.Entries {
			var uID string
			for _, pub := range m.publicProfiles {
				if strings.EqualFold(pub.Handle, e.Handle) {
					uID = pub.UserID
					break
				}
			}
			if uID != "" {
				u, uOk := m.users[uID]
				priv, pOk := m.privacySettings[uID]
				if !uOk || !pOk || u.AccountStatus != domain.AccountStatusActive || !priv.PublicProfileEnabled {
					continue
				}
			}
			eCopy := e
			eCopy.RankNo = rank
			filteredEntries = append(filteredEntries, eCopy)
			rank++
		}

		return &domain.LeaderboardResponse{
			SnapshotID:      snap.SnapshotID,
			BoardKey:        snap.BoardKey,
			Window:          snap.Window,
			Metric:          snap.Metric,
			Entries:         filteredEntries,
			DataWatermarkAt: snap.DataWatermarkAt,
		}, nil
	}

	return &domain.LeaderboardResponse{
		SnapshotID:      "",
		BoardKey:        boardKey,
		Window:          window,
		Metric:          metric,
		Entries:         []domain.LeaderboardEntry{},
		DataWatermarkAt: nil,
	}, nil
}

// --- MediaStore Implementation ---

func (m *MemoryStore) CreateAvatarUploadIntent(ctx context.Context, obj domain.UserUploadObject) (*domain.UserUploadObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	oCopy := obj
	m.uploadObjects[obj.ObjectID] = &oCopy
	return &oCopy, nil
}

func (m *MemoryStore) GetUploadObject(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	obj, ok := m.uploadObjects[objectID]
	if !ok || obj.UserID != userID {
		return nil, domain.ErrNotFound
	}
	objCopy := *obj
	return &objCopy, nil
}

func (m *MemoryStore) GetVisibleAvatar(ctx context.Context, objectID, viewerID string) (*domain.UserUploadObject, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj := m.uploadObjects[objectID]
	if obj == nil || obj.ObjectType != "avatar" || obj.UploadStatus != domain.UploadStatusReady {
		return nil, domain.ErrNotFound
	}
	u := m.users[obj.UserID]
	if u == nil || u.AccountStatus != domain.AccountStatusActive || u.AvatarObjectID == nil || *u.AvatarObjectID != objectID {
		return nil, domain.ErrNotFound
	}
	if viewerID != u.UserID {
		priv, pub := m.privacySettings[u.UserID], m.publicProfiles[u.UserID]
		if u.LeaderboardVisibility != domain.LeaderboardVisibilityPublic || priv == nil || !priv.PublicProfileEnabled || pub == nil || pub.ProfileStatus != "published" {
			return nil, domain.ErrNotFound
		}
	}
	copy := *obj
	return &copy, nil
}

func (m *MemoryStore) UpdateUploadObjectStatus(ctx context.Context, objectID string, status domain.UploadStatus, errorCode *string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.uploadObjects[objectID]
	if !ok {
		return domain.ErrNotFound
	}
	obj.UploadStatus = status
	obj.LastErrorCode = errorCode
	obj.UpdatedAt = now
	return nil
}

func (m *MemoryStore) CompleteAvatarUploadIntent(ctx context.Context, objectID, userID string, meta store.AvatarReadyMeta, now time.Time) (*domain.UserUploadObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.uploadObjects[objectID]
	if !ok || obj.UserID != userID {
		return nil, domain.ErrNotFound
	}

	// Retire previously active avatar uploads for this user
	for _, uo := range m.uploadObjects {
		if uo.UserID == userID && uo.ObjectType == "avatar" && uo.ObjectID != objectID && uo.UploadStatus == domain.UploadStatusReady {
			uo.UploadStatus = domain.UploadStatusDeletedPending
			uo.DeletedAt = &now
			uo.UpdatedAt = now
		}
	}

	obj.UploadStatus = domain.UploadStatusReady
	obj.ByteSize = &meta.ByteSize
	obj.ContentSha256 = &meta.ContentSha256
	obj.ImageWidth = &meta.ImageWidth
	obj.ImageHeight = &meta.ImageHeight
	obj.ContentType = &meta.ContentType
	obj.ReadyAt = &now
	obj.UpdatedAt = now

	if u, ok := m.users[userID]; ok {
		u.AvatarObjectID = &objectID
		avatarURL := "/api/v1/public/avatars/" + objectID
		u.AvatarURL = &avatarURL
		u.ProfileVersion++
		u.UpdatedAt = now

		if pub, ok := m.publicProfiles[userID]; ok {
			pub.AvatarURL = u.AvatarURL
			pub.SourceProfileVersion = u.ProfileVersion
			pub.ProjectionVersion++
			pub.UpdatedAt = now
		}
	}

	objCopy := *obj
	return &objCopy, nil
}

func (m *MemoryStore) ClearAvatar(ctx context.Context, userID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.users[userID]; ok {
		u.AvatarObjectID = nil
		u.AvatarURL = nil
		u.ProfileVersion++
		u.UpdatedAt = now

		if pub, ok := m.publicProfiles[userID]; ok {
			pub.AvatarURL = nil
			pub.SourceProfileVersion = u.ProfileVersion
			pub.ProjectionVersion++
			pub.UpdatedAt = now
		}
	}
	return nil
}
