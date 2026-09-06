package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"tokendance/internal/domain"
)

type contextKey string

const (
	RequestIDContextKey contextKey = "request_id"
	UserContextKey      contextKey = "auth_user"
	SessionContextKey   contextKey = "auth_session"
)

const (
	SessionCookieName = "__Host-tokendance_session"
	DevSessionCookie  = "tokendance_session"
)

type ErrorWrapper struct {
	Error ErrorPayload `json:"error"`
}

type ErrorPayload struct {
	Code       string                 `json:"code"`
	MessageKey string                 `json:"messageKey"`
	RequestID  string                 `json:"requestId"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

func GetRequestID(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDContextKey).(string); ok {
		return v
	}
	return "req_unknown"
}

func GetUserFromContext(ctx context.Context) *domain.User {
	if v, ok := ctx.Value(UserContextKey).(*domain.User); ok {
		return v
	}
	return nil
}

func GetSessionFromContext(ctx context.Context) *domain.UserSession {
	if v, ok := ctx.Value(SessionContextKey).(*domain.UserSession); ok {
		return v
	}
	return nil
}

func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := GetRequestID(r.Context())

	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		if strings.HasPrefix(r.URL.Path, "/v1/telemetry/") {
			log.Printf("[Ingest %s] rejected code=%s", reqID, appErr.Code)
		}
		status := appErr.HTTPStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(ErrorWrapper{
			Error: ErrorPayload{
				Code:       appErr.Code,
				MessageKey: appErr.MessageKey,
				RequestID:  reqID,
				Details:    appErr.Details,
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(ErrorWrapper{
		Error: ErrorPayload{
			Code:       "INTERNAL_ERROR",
			MessageKey: "api.internal",
			RequestID:  reqID,
		},
	})
}
