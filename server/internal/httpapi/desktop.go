package httpapi

import "net/http"

func (h *Handlers) AuthorizeDesktop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURI   string `json:"redirectUri"`
		CodeChallenge string `json:"codeChallenge"`
		State         string `json:"state"`
	}
	if err := decodeJSON(w, r, 4096, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	redirect, err := h.auth.AuthorizeDesktop(GetSessionFromContext(r.Context()), req.RedirectURI, req.CodeChallenge, req.State)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]string{"redirectUrl": redirect})
}

func (h *Handlers) ExchangeDesktop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"codeVerifier"`
		RedirectURI  string `json:"redirectUri"`
	}
	if err := decodeJSON(w, r, 4096, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	result, err := h.auth.ExchangeDesktop(r.Context(), req.Code, req.CodeVerifier, req.RedirectURI)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.setSessionCookie(w, result.SessionToken, result.Session.AbsoluteExpiresAt)
	WriteJSON(w, http.StatusOK, map[string]string{"csrfToken": result.CSRFToken})
}
