package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func writeCachedContent(w http.ResponseWriter, r *http.Request, data []byte, contentType, policy string) {
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	w.Header().Set("Cache-Control", policy)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writePublicCachedJSON(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "cannot encode response", http.StatusInternalServerError)
		return
	}
	writeCachedContent(w, r, data, "application/json; charset=utf-8", "public, max-age=30, must-revalidate")
}
