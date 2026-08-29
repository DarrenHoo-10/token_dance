package httpapi

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"tokendance/internal/auth"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

type Middleware struct {
	authService    *auth.Service
	trustedProxies []*net.IPNet
	redisClient    *redis.Client
	maxRateEntries int
}

func NewMiddleware(authService *auth.Service) *Middleware {
	return NewMiddlewareWithConfig(authService, config.DefaultConfig())
}

func NewMiddlewareWithConfig(authService *auth.Service, cfg *config.Config) *Middleware {
	m := &Middleware{authService: authService, maxRateEntries: cfg.RateLimitMaxEntries}
	for _, rawCIDR := range cfg.TrustedProxyCIDRs {
		_, cidr, err := net.ParseCIDR(rawCIDR)
		if err == nil {
			m.trustedProxies = append(m.trustedProxies, cidr)
		}
	}
	if cfg.RedisAddr != "" {
		m.redisClient = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond, MaxRetries: 0})
	}
	return m
}

func (m *Middleware) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range m.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(strings.TrimSpace(remoteAddr))
}

func (m *Middleware) clientIP(r *http.Request) string {
	peer := remoteIP(r.RemoteAddr)
	if peer == nil {
		return "unknown"
	}
	if !m.isTrustedProxy(peer) {
		return peer.String()
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := net.ParseIP(strings.TrimSpace(parts[i]))
			if candidate != nil && !m.isTrustedProxy(candidate) {
				return candidate.String()
			}
		}
	}
	if candidate := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); candidate != nil {
		return candidate.String()
	}
	return peer.String()
}

func (m *Middleware) RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	memoryBackend := newMemoryRateLimitBackend(m.maxRateEntries)
	var backend rateLimitBackend = memoryBackend
	if m.redisClient != nil {
		backend = fallbackRateLimitBackend{primary: &redisRateLimitBackend{client: m.redisClient}, fallback: memoryBackend}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Path + ":" + m.clientIP(r)
			allowed, err := backend.Allow(r.Context(), key, limit, window)
			if err != nil || !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(window.Seconds()))))
				WriteError(w, r, domain.NewAppError(429, "API_RATE_LIMIT_EXCEEDED", "api.rateLimited", "too many requests, please slow down", nil, domain.ErrRateLimited))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (m *Middleware) MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func (m *Middleware) RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			token, _ := crypto.GenerateOpaqueToken(13)
			reqID = "req_" + token
		}
		w.Header().Set("X-Request-Id", reqID)
		ctx := context.WithValue(r.Context(), RequestIDContextKey, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriterInterceptor{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		reqID := GetRequestID(r.Context())
		log.Printf("[%s] %s %s %d (%v)", reqID, r.Method, r.URL.Path, rw.statusCode, duration)
	})
}

type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterInterceptor) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (m *Middleware) Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC recovered in HTTP handler: %v", rec)
				WriteError(w, r, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "internal server error", nil, nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) SessionResolver(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string

		// Extract from Cookie
		if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
			token = c.Value
		} else if !m.authService.IsProduction() {
			if c, err := r.Cookie(DevSessionCookie); err == nil && c.Value != "" {
				token = c.Value
			}
		}

		if token == "" {
			// Check Authorization Bearer as optional fallback
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token != "" {
			sess, user, err := m.authService.ResolveSession(r.Context(), token)
			if err == nil && sess != nil && user != nil {
				ctx := context.WithValue(r.Context(), SessionContextKey, sess)
				ctx = context.WithValue(ctx, UserContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "authentication required", nil, domain.ErrUnauthorized))
			return
		}

		// Account status checks
		if user.AccountStatus == domain.AccountStatusSuspended {
			WriteError(w, r, domain.NewAppError(403, "ACCOUNT_SUSPENDED", "auth.accountSuspended", "account is suspended", nil, domain.ErrAccountSuspended))
			return
		}
		if user.AccountStatus == domain.AccountStatusDeleted {
			WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.accountDeleted", "account has been deleted", nil, domain.ErrAccountDeleted))
			return
		}

		// Route gating for deletion_pending
		if user.AccountStatus == domain.AccountStatusDeletionPending {
			path := r.URL.Path
			allowed := strings.HasPrefix(path, "/api/v1/me/deletion-requests") ||
				path == "/api/v1/auth/logout" ||
				path == "/api/v1/auth/session"
			if !allowed {
				WriteError(w, r, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.deletionPending", "account is pending deletion", nil, domain.ErrDeletionPending))
				return
			}
		}

		// Mandatory onboarding route gate (USR-008)
		if user.OnboardingCompletedAt == nil {
			path := r.URL.Path
			allowed := strings.HasPrefix(path, "/api/v1/auth/") ||
				path == "/api/v1/me/onboarding"
			if !allowed {
				WriteError(w, r, domain.NewAppError(403, "ONBOARDING_REQUIRED", "auth.onboardingRequired", "onboarding required before accessing this resource", map[string]interface{}{
					"onboardingRequired": true,
				}, nil))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func isAllowedOrigin(origin string, reqHost string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := u.Host
	if originHost == reqHost {
		return true
	}
	hostOnly := originHost
	if h, _, err := net.SplitHostPort(originHost); err == nil {
		hostOnly = h
	}
	reqHostOnly := reqHost
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHostOnly = h
	}
	if hostOnly == reqHostOnly {
		return true
	}
	if hostOnly == "localhost" || hostOnly == "127.0.0.1" || hostOnly == "tokendance.dev" || strings.HasSuffix(hostOnly, ".tokendance.dev") {
		return true
	}
	return false
}

func (m *Middleware) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check state-changing methods
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		sess := GetSessionFromContext(r.Context())
		if sess == nil {
			// No session, let auth middleware handle if needed
			next.ServeHTTP(w, r)
			return
		}

		// 1. Fetch Metadata check
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "cross-site request forbidden by fetch metadata", nil, domain.ErrCsrfFailed))
			return
		}

		// 2. Origin check
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isAllowedOrigin(origin, r.Host) {
				WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "origin header validation failed", nil, domain.ErrCsrfFailed))
				return
			}
		}

		// 3. X-CSRF-Token check
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" {
			WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "CSRF token missing", nil, domain.ErrCsrfFailed))
			return
		}

		if !m.authService.ValidateCSRFToken(csrfHeader, sess.CSRFTokenHash) {
			WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "CSRF token mismatch", nil, domain.ErrCsrfFailed))
			return
		}

		next.ServeHTTP(w, r)
	})
}
