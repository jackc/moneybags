// Package server translates HTTP and MCP into the same core action catalog.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
)

type Config struct {
	Origin        string
	SecureCookies bool
	AssetsDir     string
	Logger        *slog.Logger
}
type requestIDContextKey struct{}

type API struct {
	core    *core.Core
	config  Config
	actions map[string]core.ActionInfo
	rates   *limiter
	csp     string
}

func New(app *core.Core, cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	a := &API{core: app, config: cfg, actions: map[string]core.ActionInfo{}, rates: &limiter{entries: map[string]bucket{}}, csp: contentSecurityPolicy(cfg.AssetsDir)}
	for _, info := range app.Actions() {
		a.actions[info.Name] = info
	}
	r := chi.NewRouter()
	r.Use(a.headers)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/.well-known/oauth-protected-resource", a.resourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", a.resourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", a.authorizationMetadata)
	r.Get("/oauth/authorize", a.oauthAuthorize)
	r.Post("/oauth/token", a.oauthToken)
	r.Post("/oauth/revoke", a.oauthRevoke)
	r.Post("/api/actions/{action}", a.action)
	r.Post("/api/uploads", a.upload)
	r.Get("/api/attachments/{id}", a.download)
	r.Get("/api/catalog", func(w http.ResponseWriter, r *http.Request) {
		if _, e := a.webContext(r); e != nil {
			a.fail(w, e)
			return
		}
		infos := []core.ActionInfo{}
		for _, info := range app.Actions() {
			if info.Web {
				infos = append(infos, info)
			}
		}
		writeJSON(w, 200, map[string]any{"actions": infos, "limits": map[string]int{"file_bytes": domain.MaxFileBytes, "notes_bytes": domain.MaxNotesBytes, "page_size": 100}})
	})
	r.Handle("/mcp", a.mcpHandler())
	r.Handle("/mcp/*", a.mcpHandler())
	r.Handle("/*", a.staticHandler())
	return r
}

func (a *API) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [12]byte
		_, _ = rand.Read(id[:])
		requestID := hex.EncodeToString(id[:])
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", a.csp)
		if a.config.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/oauth/") || strings.HasPrefix(r.URL.Path, "/mcp") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) csrf(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != a.config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		a.fail(w, core.E("permission_denied", "Request origin is not allowed"))
		return false
	}
	return true
}

func (a *API) webContext(r *http.Request) (context.Context, error) {
	cookie, e := r.Cookie("moneybags_session")
	if e != nil {
		return r.Context(), core.E("unauthorized", "Sign in to continue")
	}
	b, _ := json.Marshal(core.TokenParams{Token: cookie.Value})
	raw, e := a.core.InvokeJSON(r.Context(), "authenticate_session", b)
	if e != nil {
		return r.Context(), core.E("unauthorized", "Sign in to continue")
	}
	var p core.Principal
	if e = json.Unmarshal(raw, &p); e != nil {
		return r.Context(), e
	}
	return core.WithPrincipal(r.Context(), p), nil
}

func (a *API) action(w http.ResponseWriter, r *http.Request) {
	if !a.csrf(w, r) {
		return
	}
	name := chi.URLParam(r, "action")
	info, ok := a.actions[name]
	if !ok || !info.Web {
		a.fail(w, core.E("not_found", "Unknown action"))
		return
	}
	ctx := r.Context()
	if !info.Public {
		var e error
		ctx, e = a.webContext(r)
		if e != nil {
			a.fail(w, e)
			return
		}
	}
	if !a.limit(w, r, ctx, info.Public) {
		return
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		a.fail(w, core.E("validation_error", "Use application/json"))
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(info.MaxPayloadBytes)))
	if e != nil {
		a.fail(w, core.E("validation_error", "Request exceeds payload limit"))
		return
	}
	start := time.Now()
	out, e := a.core.InvokeJSON(ctx, name, raw)
	status := "ok"
	if e != nil {
		status = "error"
	}
	a.config.Logger.Info("action", "request_id", w.Header().Get("X-Request-ID"), "action", name, "source", "web", "duration_ms", time.Since(start).Milliseconds(), "status", status)
	if e != nil {
		a.fail(w, e)
		return
	}
	switch name {
	case "register", "login", "accept_invitation", "finish_passkey_login":
		var session core.AuthSession
		if e = json.Unmarshal(out, &session); e != nil {
			a.fail(w, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "moneybags_session", Value: session.Token, Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: int(time.Until(session.ExpiresAt).Seconds())})
		writeJSON(w, 200, map[string]any{"user": session.User, "family": session.Family, "expires_at": session.ExpiresAt})
		return
	case "logout":
		http.SetCookie(w, &http.Cookie{Name: "moneybags_session", Value: "", Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
	writeRaw(w, 200, out)
}

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	if !a.csrf(w, r) {
		return
	}
	ctx, e := a.webContext(r)
	if e != nil {
		a.fail(w, e)
		return
	}
	if !a.limit(w, r, ctx, false) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxFileBytes+(64<<10))
	if e = r.ParseMultipartForm(domain.MaxFileBytes + (64 << 10)); e != nil {
		a.fail(w, core.E("validation_error", "File must be at most 5 MiB"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, e := r.FormFile("file")
	if e != nil {
		a.fail(w, core.E("validation_error", "A file is required"))
		return
	}
	defer file.Close()
	data, e := io.ReadAll(io.LimitReader(file, domain.MaxFileBytes+1))
	if e != nil || len(data) > domain.MaxFileBytes {
		a.fail(w, core.E("validation_error", "File must be at most 5 MiB"))
		return
	}
	raw, _ := json.Marshal(map[string]any{"request_id": r.FormValue("request_id"), "file_name": header.Filename, "mime_type": http.DetectContentType(data), "data": data})
	out, e := a.core.InvokeJSON(ctx, "stage_attachment", raw)
	if e != nil {
		a.fail(w, e)
		return
	}
	writeRaw(w, 200, out)
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	ctx, e := a.webContext(r)
	if e != nil {
		a.fail(w, e)
		return
	}
	if !a.limit(w, r, ctx, false) {
		return
	}
	raw, _ := json.Marshal(map[string]string{"attachment_id": chi.URLParam(r, "id")})
	out, e := a.core.InvokeJSON(ctx, "get_attachment", raw)
	if e != nil {
		a.fail(w, e)
		return
	}
	var result struct {
		Data       []byte            `json:"data"`
		FileName   string            `json:"file_name"`
		MIMEType   string            `json:"mime_type"`
		Attachment domain.Attachment `json:"attachment"`
	}
	if e = json.Unmarshal(out, &result); e != nil {
		a.fail(w, e)
		return
	}
	if result.FileName == "" {
		result.FileName = result.Attachment.FileName
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.FileName}))
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.WriteHeader(200)
	_, _ = w.Write(result.Data)
}

func (a *API) fail(w http.ResponseWriter, err error) {
	var oauth *core.OAuthError
	if errors.As(err, &oauth) {
		err = core.E("validation_error", oauth.Description)
	}
	var e *core.Error
	if !errors.As(err, &e) {
		a.config.Logger.Error("action failed", "request_id", w.Header().Get("X-Request-ID"), "error_type", fmtErrorType(err))
		e = core.E("internal_error", "The operation could not be completed")
	}
	status := 400
	switch e.Code {
	case "unauthorized", "invalid_credentials":
		status = 401
	case "permission_denied", "recent_authentication_required":
		status = 403
	case "not_found", "attachment_unavailable":
		status = 404
	case "version_conflict", "idempotency_conflict", "bag_archived", "username_unavailable":
		status = 409
	case "rate_limited":
		status = 429
	case "internal_error":
		status = 500
	}
	writeJSON(w, status, map[string]any{"error": e})
}
func fmtErrorType(e error) string {
	if e == nil {
		return "none"
	}
	return "internal"
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, e := json.Marshal(v)
	if e != nil {
		http.Error(w, "encoding error", 500)
		return
	}
	writeRaw(w, status, b)
}
func writeRaw(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (a *API) staticHandler() http.Handler {
	dir := a.config.AssetsDir
	if dir == "" {
		dir = "build/assets"
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/oauth/") {
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if strings.Contains(clean, "/.") {
			http.NotFound(w, r)
			return
		}
		if _, e := os.Stat(dir + clean); e != nil {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			fs.ServeHTTP(w, clone)
			return
		}
		if strings.HasPrefix(clean, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

type bucket struct {
	count   int
	expires time.Time
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]bucket
}

func (l *limiter) allow(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.entries) > 10000 {
		for k, v := range l.entries {
			if now.After(v.expires) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) > 10000 {
			return false
		}
	}
	b := l.entries[key]
	if now.After(b.expires) {
		b = bucket{expires: now.Add(time.Minute)}
	}
	b.count++
	l.entries[key] = b
	return b.count <= max
}
func (a *API) limit(w http.ResponseWriter, r *http.Request, ctx context.Context, public bool) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := "ip:" + host
	max := 120
	if p, ok := core.PrincipalFromContext(ctx); ok {
		key = "user:" + p.UserID
	}
	if public {
		key = "auth:" + host
		max = 20
	}
	if a.rates.allow(key, max) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	a.fail(w, core.E("rate_limited", "Too many requests; try again shortly"))
	return false
}

// SvelteKit's static adapter includes an inline bootstrap. Hash its exact bytes
// from the trusted release artifact; blanket unsafe-inline is unnecessary.
func contentSecurityPolicy(assets string) string {
	if assets == "" {
		assets = "build/assets"
	}
	sources := []string{"'self'"}
	if data, err := os.ReadFile(filepath.Join(assets, "index.html")); err == nil {
		scripts := regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>`).FindAllSubmatch(data, -1)
		for _, script := range scripts {
			if len(script[1]) == 0 {
				continue
			}
			hash := sha256.Sum256(script[1])
			sources = append(sources, "'sha256-"+base64.StdEncoding.EncodeToString(hash[:])+"'")
		}
	}
	return "default-src 'self'; script-src " + strings.Join(sources, " ") + "; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}
