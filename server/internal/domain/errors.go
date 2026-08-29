package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound              = errors.New("resource not found")
	ErrUnauthorized          = errors.New("unauthorized")
	ErrForbidden             = errors.New("forbidden")
	ErrConflict              = errors.New("conflict")
	ErrInvalidArgument       = errors.New("invalid argument")
	ErrPreconditionFailed    = errors.New("precondition failed")
	ErrRateLimited           = errors.New("rate limited")
	ErrInternal              = errors.New("internal server error")
	ErrAccountSuspended      = errors.New("account suspended")
	ErrDeletionPending       = errors.New("account deletion pending")
	ErrAccountDeleted        = errors.New("account deleted")
	ErrInvalidCredentials    = errors.New("invalid email or password")
	ErrAccountExists         = errors.New("account already exists")
	ErrHandleTaken           = errors.New("handle is already taken")
	ErrInvalidHandle         = errors.New("invalid handle format or reserved word")
	ErrDeviceRevoked         = errors.New("device is revoked")
	ErrDeviceDisabled        = errors.New("device is disabled")
	ErrPublicKeyConflict     = errors.New("device public key conflict")
	ErrChallengeExpired      = errors.New("challenge expired")
	ErrChallengeLocked       = errors.New("challenge locked due to max attempts")
	ErrChallengeInvalid      = errors.New("invalid verification code")
	ErrIdempotencyReused     = errors.New("idempotency key reused with different payload")
	ErrQueryWatermarkChanged = errors.New("query watermark changed")
	ErrCsrfFailed            = errors.New("csrf validation failed")
	ErrDirtyBaseline         = errors.New("dirty baseline data detected in preflight guard")
)

type AppError struct {
	Code       string                 `json:"code"`
	MessageKey string                 `json:"messageKey"`
	Message    string                 `json:"message,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	HTTPStatus int                    `json:"-"`
	Err        error                  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("[%s] %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func NewAppError(status int, code, messageKey, message string, details map[string]interface{}, underlying error) *AppError {
	return &AppError{
		HTTPStatus: status,
		Code:       code,
		MessageKey: messageKey,
		Message:    message,
		Details:    details,
		Err:        underlying,
	}
}
