package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/tokendance/token-collector/server/internal/store"
)

type UserSessionResolver interface {
	ResolveUserSession(ctx context.Context, authorization string) (string, error)
}

type StoredUserSessionResolver struct {
	Users store.UserStore
}

func (r *StoredUserSessionResolver) ResolveUserSession(ctx context.Context, authorization string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", fmt.Errorf("missing bearer user session")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	if token == "" {
		return "", fmt.Errorf("empty bearer user session")
	}
	user, err := r.Users.GetUserByAuthSubjectHash(ctx, sha256.Sum256([]byte(token)))
	if err != nil {
		return "", fmt.Errorf("invalid user session: %w", err)
	}
	if user.AccountStatus != "active" {
		return "", fmt.Errorf("user account is %s", user.AccountStatus)
	}
	return user.UserID, nil
}
