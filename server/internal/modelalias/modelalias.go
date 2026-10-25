// Package modelalias reads the deployment-managed model folding rules: exact
// alias pairs published to clients through /v1/model-aliases and applied to
// the server-side analytics folding, so both fold the same spellings without
// a client release. The rules are public deployment configuration, never a
// credential or release artifact; the file is read on every call so an
// atomically replaced file takes effect immediately.
package modelalias

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const (
	MaxFileBytes  = 256 * 1024
	MaxNameLength = 256
	MaxRules      = 4096
)

var (
	ErrInvalidName   = errors.New("modelalias: rule name is empty, oversized, or outside the model-id charset")
	ErrIdentityRule  = errors.New("modelalias: rule folds a spelling onto itself")
	ErrDuplicateRule = errors.New("modelalias: rule folds the same spelling twice")
	ErrTooManyRules  = errors.New("modelalias: too many rules")
	ErrOversized     = errors.New("modelalias: configuration file exceeds the size limit")
)

// Alias folds one spelling (`from`) onto a canonical model name (`to`).
type Alias struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Config is the published document; Revision bumps when the set changes.
type Config struct {
	Revision uint64  `json:"revision"`
	Aliases  []Alias `json:"aliases"`
}

// Read loads the alias configuration. An unset TOKENDANCE_MODEL_ALIAS_FILE is
// the normal "no rules" state; a configured file that is missing, oversized,
// or invalid is an error so the misconfiguration stays visible.
func Read() (Config, error) {
	path := strings.TrimSpace(os.Getenv("TOKENDANCE_MODEL_ALIAS_FILE"))
	if path == "" {
		return Config{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > MaxFileBytes {
		return Config{}, ErrOversized
	}
	return Parse(data)
}

// Parse validates and normalizes a configuration document: names are trimmed
// and lowercased within the model-id charset, every rule folds distinct
// non-empty spellings, and from spellings stay unique. Matching semantics are
// exact: a rule applies when its from equals the lowercased id, or the id's
// final `/models/` segment, either before or after the Fireworks dot decode
// (see the folding in internal/store/mysql).
func Parse(data []byte) (Config, error) {
	var raw struct {
		Revision uint64  `json:"revision"`
		Aliases  []Alias `json:"aliases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, err
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		if err != nil {
			return Config{}, err
		}
		return Config{}, io.ErrUnexpectedEOF
	}
	if len(raw.Aliases) > MaxRules {
		return Config{}, ErrTooManyRules
	}
	aliases := make([]Alias, 0, len(raw.Aliases))
	seen := make(map[string]bool, len(raw.Aliases))
	for _, rule := range raw.Aliases {
		from, to := normalizeName(rule.From), normalizeName(rule.To)
		if !validName(from) || !validName(to) {
			return Config{}, ErrInvalidName
		}
		if from == to {
			return Config{}, ErrIdentityRule
		}
		if seen[from] {
			return Config{}, ErrDuplicateRule
		}
		seen[from] = true
		aliases = append(aliases, Alias{From: from, To: to})
	}
	return Config{Revision: raw.Revision, Aliases: aliases}, nil
}

// Map indexes the rules for exact-match folding lookups; nil when empty.
func (c Config) Map() map[string]string {
	if len(c.Aliases) == 0 {
		return nil
	}
	m := make(map[string]string, len(c.Aliases))
	for _, rule := range c.Aliases {
		m[rule.From] = rule.To
	}
	return m
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// validName keeps the charset to what model ids use; anything else in the
// deployment file is a mistake we refuse rather than fold.
func validName(name string) bool {
	if name == "" || len(name) > MaxNameLength {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '@' || r == ':' || r == '+' || r == '~':
		default:
			return false
		}
	}
	return true
}
