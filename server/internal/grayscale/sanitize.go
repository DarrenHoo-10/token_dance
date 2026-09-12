package grayscale

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	safeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	handlePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
	mirrorKey      = []byte("tokendance-grayscale-mirror-v1")
)

func MirrorHash(kind, id string) []byte {
	h := hmac.New(sha256.New, mirrorKey)
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(id))
	return h.Sum(nil)
}

func MirrorHandle(userID string) string {
	sum := sha256.Sum256([]byte("grayscale-handle:" + userID))
	return "g" + hex.EncodeToString(sum[:8])
}

func chooseHandle(existing string, userID string, taken map[string]string) string {
	candidate := strings.ToLower(strings.TrimSpace(existing))
	if handlePattern.MatchString(candidate) {
		if owner, ok := taken[candidate]; !ok || owner == userID {
			return candidate
		}
	}
	generated := MirrorHandle(userID)
	if owner, ok := taken[generated]; ok && owner != userID {
		return "g" + hex.EncodeToString(MirrorHash("handle-fallback", userID))[:16]
	}
	return generated
}

func valueAt(columns []string, values []any, name string) any {
	index := columnIndex(columns, name)
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}

func asString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func columnIndex(columns []string, name string) int {
	for index, column := range columns {
		if column == name {
			return index
		}
	}
	return -1
}

func setValue(columns []string, values []any, name string, value any) {
	if index := columnIndex(columns, name); index >= 0 {
		values[index] = value
	}
}

func sanitizeUserRow(columns []string, values []any, takenHandles map[string]string) string {
	userID := asString(valueAt(columns, values, "user_id"))
	setValue(columns, values, "auth_subject_hash", MirrorHash("auth", userID))
	setValue(columns, values, "email_lookup_hash", MirrorHash("email", userID))
	setValue(columns, values, "email_ciphertext", nil)
	setValue(columns, values, "email_verified_at", nil)
	setValue(columns, values, "avatar_object_id", nil)
	if asString(valueAt(columns, values, "account_status")) == "deleted" {
		setValue(columns, values, "account_status", "active")
	}
	handle := chooseHandle(asString(valueAt(columns, values, "handle")), userID, takenHandles)
	setValue(columns, values, "handle", handle)
	takenHandles[handle] = userID
	return handle
}

func sanitizeInstallationRow(columns []string, values []any) {
	installationID := asString(valueAt(columns, values, "installation_id"))
	setValue(columns, values, "device_public_key", MirrorHash("device", installationID))
}

func sanitizePublicProfileRow(columns []string, values []any, handle string) {
	setValue(columns, values, "handle", handle)
	if !asBool(valueAt(columns, values, "show_bio")) {
		setValue(columns, values, "bio", nil)
	}
	if asString(valueAt(columns, values, "profile_status")) == "" {
		setValue(columns, values, "profile_status", "published")
	}
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case []byte:
		return string(typed) == "1" || strings.EqualFold(string(typed), "true")
	case string:
		return typed == "1" || strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func printableSummary(ids []string) string {
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		cleaned = append(cleaned, strings.Map(func(r rune) rune {
			if unicode.IsPrint(r) {
				return r
			}
			return -1
		}, id))
	}
	return strings.Join(cleaned, ",")
}
