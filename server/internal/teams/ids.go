package teams

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

func NewPrefixedID(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", err
	}
	id := prefix + token
	if len(id) != 30 {
		return "", fmt.Errorf("team id length %d != 30", len(id))
	}
	return id, nil
}

func NewInviteToken() (string, []byte, error) {
	raw, err := crypto.GenerateRandomBytes(domain.InviteLinkTokenBytes)
	if err != nil {
		return "", nil, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), raw, nil
}

func HashInviteToken(raw []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("tokendance.team-invite-link.v1\x00"))
	h.Write(raw)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func DecodeInviteToken(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) != domain.InviteLinkTokenBytes {
		return nil, domain.ErrInvalidArgument
	}
	return raw, nil
}

func GraphemeCount(s string) int {
	return utf8.RuneCountInString(s)
}

func NormalizeTeamName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	n := GraphemeCount(trimmed)
	if n < domain.TeamNameMinGraphemes || n > domain.TeamNameMaxGraphemes {
		return "", domain.NewAppError(400, "API_INVALID_ARGUMENT", "teams.invalidName", "team name must be 2-40 graphemes", map[string]interface{}{
			"fieldErrors": map[string]string{"name": "teams.invalidName"},
		}, domain.ErrInvalidArgument)
	}
	if len(trimmed) > domain.TeamNameMaxUTF8Bytes {
		return "", domain.NewAppError(400, "API_INVALID_ARGUMENT", "teams.invalidName", "team name exceeds utf-8 byte limit", nil, domain.ErrInvalidArgument)
	}
	return trimmed, nil
}

func NormalizeTeamDescription(desc string) (string, error) {
	trimmed := strings.TrimSpace(desc)
	if GraphemeCount(trimmed) > domain.TeamDescMaxGraphemes {
		return "", domain.NewAppError(400, "API_INVALID_ARGUMENT", "teams.invalidDescription", "team description must be 0-120 graphemes", map[string]interface{}{
			"fieldErrors": map[string]string{"description": "teams.invalidDescription"},
		}, domain.ErrInvalidArgument)
	}
	if len(trimmed) > domain.TeamDescMaxUTF8Bytes {
		return "", domain.NewAppError(400, "API_INVALID_ARGUMENT", "teams.invalidDescription", "team description exceeds utf-8 byte limit", nil, domain.ErrInvalidArgument)
	}
	return trimmed, nil
}
