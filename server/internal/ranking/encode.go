package ranking

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	TokenDigits       = 30
	RegisteredAtWidth = 16
	memberSep         = "|"
	userPayloadSep    = "\t"
	RuleVersion       = "1"
	HotSize           = 2000
	PublicCap         = 1000
	oldSnapshotTTL    = 30 * time.Minute
)

var (
	ErrNegativeToken       = errors.New("ranking token must not be negative")
	ErrTokenOverflow       = errors.New("ranking token exceeds 30-digit encoding")
	ErrInvalidMember       = errors.New("invalid ranking member")
	ErrInvalidWindow       = errors.New("invalid ranking window")
	ErrInvalidUserID       = errors.New("invalid ranking user id")
	ErrInvalidRegisteredAt = errors.New("invalid ranking registered_at")
)

var maxToken30 = func() *big.Int {
	v := new(big.Int).Exp(big.NewInt(10), big.NewInt(TokenDigits), nil)
	return v.Sub(v, big.NewInt(1))
}()

func EncodeToken(tokens *big.Int) (string, error) {
	if tokens == nil || tokens.Sign() < 0 {
		return "", ErrNegativeToken
	}
	if tokens.Cmp(maxToken30) > 0 {
		return "", ErrTokenOverflow
	}
	digits := tokens.String()
	if len(digits) > TokenDigits {
		return "", ErrTokenOverflow
	}
	out := make([]byte, TokenDigits)
	pad := TokenDigits - len(digits)
	for i := 0; i < TokenDigits; i++ {
		d := byte(0)
		if i >= pad {
			c := digits[i-pad]
			if c < '0' || c > '9' {
				return "", ErrTokenOverflow
			}
			d = c - '0'
		}
		out[i] = '0' + (9 - d)
	}
	return string(out), nil
}

func EncodeTokenUint64(tokens uint64) (string, error) {
	return EncodeToken(new(big.Int).SetUint64(tokens))
}

func DecodeToken(encoded string) (*big.Int, error) {
	if len(encoded) != TokenDigits {
		return nil, ErrInvalidMember
	}
	digits := make([]byte, TokenDigits)
	for i := 0; i < TokenDigits; i++ {
		c := encoded[i]
		if c < '0' || c > '9' {
			return nil, ErrInvalidMember
		}
		digits[i] = '0' + (9 - (c - '0'))
	}
	v := new(big.Int)
	if _, ok := v.SetString(string(digits), 10); !ok {
		return nil, ErrInvalidMember
	}
	return v, nil
}

func encodeRegisteredAt(ts time.Time) (string, error) {
	if ts.IsZero() {
		return "", ErrInvalidRegisteredAt
	}
	ms := ts.UTC().UnixMilli()
	if ms < 0 {
		return "", ErrInvalidRegisteredAt
	}
	s := strconv.FormatInt(ms, 10)
	if len(s) > RegisteredAtWidth {
		return "", ErrInvalidRegisteredAt
	}
	return fmt.Sprintf("%0*d", RegisteredAtWidth, ms), nil
}

func decodeRegisteredAt(encoded string) (time.Time, error) {
	if len(encoded) != RegisteredAtWidth {
		return time.Time{}, ErrInvalidMember
	}
	ms, err := strconv.ParseInt(encoded, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, ErrInvalidMember
	}
	return time.UnixMilli(ms).UTC(), nil
}

func EncodeMember(tokens uint64, registeredAt time.Time, userID string) (string, error) {
	if err := validateUserID(userID); err != nil {
		return "", err
	}
	tokenKey, err := EncodeTokenUint64(tokens)
	if err != nil {
		return "", err
	}
	timeKey, err := encodeRegisteredAt(registeredAt)
	if err != nil {
		return "", err
	}
	return tokenKey + memberSep + timeKey + memberSep + userID, nil
}

func DecodeMember(member string) (tokens uint64, registeredAt time.Time, userID string, err error) {
	tokenKey, timeKey, userID, err := splitMember(member)
	if err != nil {
		return 0, time.Time{}, "", err
	}
	value, err := DecodeToken(tokenKey)
	if err != nil {
		return 0, time.Time{}, "", err
	}
	if !value.IsUint64() {
		return 0, time.Time{}, "", ErrTokenOverflow
	}
	registeredAt, err = decodeRegisteredAt(timeKey)
	if err != nil {
		return 0, time.Time{}, "", err
	}
	return value.Uint64(), registeredAt, userID, nil
}

func splitMember(member string) (tokenKey, timeKey, userID string, err error) {
	first := strings.IndexByte(member, memberSep[0])
	if first <= 0 {
		return "", "", "", ErrInvalidMember
	}
	second := strings.IndexByte(member[first+1:], memberSep[0])
	if second <= 0 {
		return "", "", "", ErrInvalidMember
	}
	second += first + 1
	tokenKey = member[:first]
	timeKey = member[first+1 : second]
	userID = member[second+1:]
	if len(tokenKey) != TokenDigits || len(timeKey) != RegisteredAtWidth {
		return "", "", "", ErrInvalidMember
	}
	if err := validateUserID(userID); err != nil {
		return "", "", "", err
	}
	return tokenKey, timeKey, userID, nil
}

func memberUserID(member string) (string, error) {
	_, _, userID, err := splitMember(member)
	return userID, err
}

func validateUserID(userID string) error {
	if userID == "" || strings.Contains(userID, memberSep) || strings.Contains(userID, userPayloadSep) {
		return ErrInvalidUserID
	}
	return nil
}

func encodeUserPayload(revision uint64, tokens uint64, member string) string {
	return strconv.FormatUint(revision, 10) + userPayloadSep + strconv.FormatUint(tokens, 10) + userPayloadSep + member
}

func parseUserPayload(payload string) (revision uint64, tokens uint64, member string, err error) {
	first := strings.IndexByte(payload, '\t')
	if first <= 0 {
		return 0, 0, "", ErrInvalidMember
	}
	second := strings.IndexByte(payload[first+1:], '\t')
	if second < 0 {
		return 0, 0, "", ErrInvalidMember
	}
	second += first + 1
	revision, err = strconv.ParseUint(payload[:first], 10, 64)
	if err != nil {
		return 0, 0, "", ErrInvalidMember
	}
	tokens, err = strconv.ParseUint(payload[first+1:second], 10, 64)
	if err != nil {
		return 0, 0, "", ErrInvalidMember
	}
	member = payload[second+1:]
	if member == "" {
		return 0, 0, "", ErrInvalidMember
	}
	return revision, tokens, member, nil
}
