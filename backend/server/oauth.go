package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/moneybags/backend/core"
)

func (a *API) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"resource": a.config.Origin + "/mcp", "authorization_servers": []string{a.config.Origin}, "scopes_supported": []string{"bags:read", "bags:write", "family:write"}, "bearer_methods_supported": []string{"header"}, "resource_name": "Money Bags"})
}
func (a *API) authorizationMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"issuer": a.config.Origin, "authorization_endpoint": a.config.Origin + "/oauth/authorize", "token_endpoint": a.config.Origin + "/oauth/token", "revocation_endpoint": a.config.Origin + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"bags:read", "bags:write", "family:write"}, "client_id_metadata_document_supported": true})
}

// The SPA logs in when necessary and explicitly consents. Core validates client
// metadata, exact callback, scope, audience and PKCE before a code is issued.
func (a *API) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 16<<10 {
		http.Error(w, "request too large", 400)
		return
	}
	http.Redirect(w, r, "/authorize?"+r.URL.RawQuery, http.StatusFound)
}
func (a *API) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !a.limit(w, r, r.Context(), true) {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.config.Origin {
		a.oauthError(w, &core.OAuthError{Code: "invalid_request", Description: "Origin is not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if e := r.ParseForm(); e != nil {
		a.oauthError(w, &core.OAuthError{Code: "invalid_request", Description: "Invalid form"})
		return
	}
	var name string
	var params any
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		name = "exchange_oauth_code"
		params = core.ExchangeOAuthCodeParams{ClientID: r.PostForm.Get("client_id"), Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"), CodeVerifier: r.PostForm.Get("code_verifier"), Resource: r.PostForm.Get("resource")}
	case "refresh_token":
		name = "refresh_oauth_token"
		params = core.RefreshOAuthTokenParams{ClientID: r.PostForm.Get("client_id"), RefreshToken: r.PostForm.Get("refresh_token"), Resource: r.PostForm.Get("resource")}
	default:
		a.oauthError(w, &core.OAuthError{Code: "unsupported_grant_type", Description: "Use authorization_code or refresh_token"})
		return
	}
	raw, _ := json.Marshal(params)
	out, e := a.core.InvokeJSON(r.Context(), name, raw)
	if e != nil {
		a.oauthError(w, e)
		return
	}
	writeRaw(w, 200, out)
}
func (a *API) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if !a.limit(w, r, r.Context(), true) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if e := r.ParseForm(); e != nil {
		a.oauthError(w, &core.OAuthError{Code: "invalid_request", Description: "Invalid form"})
		return
	}
	raw, _ := json.Marshal(core.RevokeOAuthTokenParams{Token: r.PostForm.Get("token"), ClientID: r.PostForm.Get("client_id"), TokenTypeHint: r.PostForm.Get("token_type_hint")})
	_, e := a.core.InvokeJSON(r.Context(), "revoke_oauth_token", raw)
	if e != nil {
		a.oauthError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{})
}
func (a *API) oauthError(w http.ResponseWriter, err error) {
	var oauth *core.OAuthError
	if errors.As(err, &oauth) {
		writeJSON(w, 400, oauth)
		return
	}
	var app *core.Error
	if errors.As(err, &app) {
		writeJSON(w, 400, map[string]string{"error": "invalid_request", "error_description": app.Message})
		return
	}
	a.config.Logger.Error("OAuth operation failed", "request_id", w.Header().Get("X-Request-ID"))
	writeJSON(w, 500, map[string]string{"error": "server_error", "error_description": "The authorization request could not be completed"})
}
