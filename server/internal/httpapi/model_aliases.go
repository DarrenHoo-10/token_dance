package httpapi

import (
	"net/http"

	"tokendance/internal/modelalias"
)

// GetModelAliases serves the deployment-managed model folding rules to
// clients. An unset rules file is the normal empty state; a configured but
// unreadable or invalid file fails closed like the update policy so the
// misconfiguration is visible instead of silently ignored.
func (h *Handlers) GetModelAliases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cfg, err := modelalias.Read()
	if err != nil {
		http.Error(w, "model aliases unavailable", http.StatusServiceUnavailable)
		return
	}
	if cfg.Aliases == nil {
		cfg.Aliases = []modelalias.Alias{}
	}
	WriteJSON(w, http.StatusOK, cfg)
}
