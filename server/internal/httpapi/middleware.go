package httpapi

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"

	"tokendance/internal/auth"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

type Middleware struct {
	authService *auth.Service
}

func NewMiddleware(authService *auth.Service) *Middleware {
	return &Middleware{
		authService: authService,
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
		} else if c, err := r.Cookie(DevSessionCookie); err == nil && c.Value != "" {
			token = c.Value
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

		next.ServeHTTP(w, r)
	})
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

		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" {
			WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "CSRF token missing", nil, domain.ErrCsrfFailed))
			return
		}

		expectedHash := m.authService.ComputeTokenHash(csrfHeader)
		if subtle.ConstantTimeCompare(expectedHash[:], sess.CSRFTokenHash[:]) != 1 {
			WriteError(w, r, domain.NewAppError(403, "CSRF_VALIDATION_FAILED", "auth.csrfFailed", "CSRF token mismatch", nil, domain.ErrCsrfFailed))
			return
		}

		next.ServeHTTP(w, r)
	})
}
