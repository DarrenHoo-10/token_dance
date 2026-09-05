package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"strconv"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

// Short-lived, one-use handoffs are local to this API process. A restart asks
// the desktop to retry; no browser session token is exposed to the webpage.
type desktopCode struct {
	sessionHash [32]byte
	challenge   string
	redirectURI string
	expiresAt   time.Time
}

func desktopError() error {
	return domain.NewAppError(400, "DESKTOP_LOGIN_INVALID", "auth.desktopInvalid", "desktop login expired or invalid", nil, nil)
}

func validDesktopHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validDesktopRedirect(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && u.Scheme == "http" &&
		u.Hostname() == "127.0.0.1" && u.User == nil && u.Path == "/callback" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == ""
}

func (s *Service) AuthorizeDesktop(session *domain.UserSession, redirectURI, challenge, state string) (string, error) {
	if session == nil || !validDesktopRedirect(redirectURI) || !validDesktopHex(challenge) || !validDesktopHex(state) {
		return "", desktopError()
	}
	token, err := crypto.GenerateOpaqueToken(32)
	if err != nil {
		return "", err
	}
	now := s.clk.Now()
	s.desktopMu.Lock()
	defer s.desktopMu.Unlock()
	if s.desktopCodes == nil {
		s.desktopCodes = make(map[[32]byte]desktopCode)
	}
	for key, code := range s.desktopCodes {
		if !now.Before(code.expiresAt) {
			delete(s.desktopCodes, key)
		}
	}
	if len(s.desktopCodes) >= 1024 {
		return "", domain.NewAppError(429, "RATE_LIMITED", "errors.unknown", "too many pending desktop logins", nil, nil)
	}
	s.desktopCodes[sha256.Sum256([]byte(token))] = desktopCode{session.SessionTokenHash, challenge, redirectURI, now.Add(2 * time.Minute)}
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set("code", token)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Service) ExchangeDesktop(ctx context.Context, code, verifier, redirectURI string) (*LoginResult, error) {
	if len(code) > 128 || !validDesktopHex(verifier) || !validDesktopRedirect(redirectURI) {
		return nil, desktopError()
	}
	now := s.clk.Now()
	key := sha256.Sum256([]byte(code))
	proof := sha256.Sum256([]byte(verifier))
	s.desktopMu.Lock()
	pending, ok := s.desktopCodes[key]
	valid := ok && now.Before(pending.expiresAt) && pending.redirectURI == redirectURI &&
		subtle.ConstantTimeCompare([]byte(pending.challenge), []byte(hex.EncodeToString(proof[:]))) == 1
	if valid || (ok && !now.Before(pending.expiresAt)) {
		delete(s.desktopCodes, key)
	}
	s.desktopMu.Unlock()
	if !valid {
		return nil, desktopError()
	}

	// Revalidate the source session, including account/credential revocation.
	source, user, err := s.store.ResolveSession(ctx, pending.sessionHash, now)
	if err != nil {
		return nil, desktopError()
	}
	token, err := crypto.GenerateOpaqueToken(32)
	if err != nil {
		return nil, err
	}
	id, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return nil, err
	}
	eventID, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return nil, err
	}
	csrf := s.deriveCSRFToken(token, s.cfg.CSRFKeys.CurrentVersion)
	label := "TokenDance Desktop"
	session := domain.UserSession{
		SessionID: "ses_" + id, UserID: user.UserID,
		SessionTokenHash: hashWithVersion(s.cfg.SessionKeys, s.cfg.SessionKeys.CurrentVersion, token),
		CSRFTokenHash:    s.ComputeCSRFHash(csrf), CredentialVersion: source.CredentialVersion,
		SessionStatus: domain.SessionStatusActive, DeviceLabel: &label, LastSeenAt: now,
		IdleExpiresAt: now.Add(s.cfg.SessionIdleTTL), AbsoluteExpiresAt: now.Add(s.cfg.SessionAbsoluteTTL),
		CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.store.CreateSessionTx(ctx, session, domain.UserSecurityEvent{
		EventID: "sev_" + eventID, UserID: &user.UserID, SessionID: &session.SessionID,
		EventType: "login_succeeded", Outcome: "success", CreatedAt: now,
		MetadataJSON: map[string]interface{}{"device": label, "method": "browser_handoff"},
	})
	if err != nil {
		return nil, err
	}
	return &LoginResult{Session: created, SessionToken: token, CSRFToken: csrf, User: user}, nil
}
