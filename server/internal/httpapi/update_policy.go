package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// The policy is public deployment configuration, never a credential or release artifact.
// Read on every request so an atomically replaced configuration file takes effect immediately.
type updatePolicy struct {
	MinimumVersion      string `json:"minimumVersion"`
	RefreshAfterSeconds int    `json:"refreshAfterSeconds"`
}

var stableClientVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func readUpdatePolicy() (updatePolicy, error) {
	p := updatePolicy{MinimumVersion: strings.TrimSpace(os.Getenv("TOKENDANCE_MIN_CLIENT_VERSION")), RefreshAfterSeconds: 60}
	if path := os.Getenv("TOKENDANCE_UPDATE_POLICY_FILE"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			return p, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return p, err
		}
		if len(data) > 4096 {
			return p, io.ErrShortBuffer
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&p); err != nil {
			return p, err
		}
		var tail any
		if err := decoder.Decode(&tail); err != io.EOF {
			if err != nil {
				return p, err
			}
			return p, io.ErrUnexpectedEOF
		}
	}
	p.RefreshAfterSeconds = 60
	return p, nil
}

func (h *Handlers) GetUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, err := readUpdatePolicy()
	if err != nil || (p.MinimumVersion != "" && !stableClientVersion.MatchString(p.MinimumVersion)) {
		http.Error(w, "update policy unavailable", http.StatusServiceUnavailable)
		return
	}
	WriteJSON(w, http.StatusOK, p)
}
