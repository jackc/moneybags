package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const OAuthAccessTokenLifespan = time.Hour
const OAuthRefreshTokenLifespan = 30 * 24 * time.Hour

var pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type OAuthError struct {
	Code         string `json:"error"`
	Description  string `json:"error_description"`
	Redirectable bool   `json:"-"`
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }
func oauthFailure(code, description string) error {
	return &OAuthError{Code: code, Description: description}
}

type OAuthAuthorizationParams struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
}
type OAuthAuthorizationRequest struct {
	Client   OAuthClient `json:"client"`
	Scope    string      `json:"scope"`
	Audience string      `json:"audience"`
}
type OAuthAuthorizationCodeResult struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
	State       string `json:"state"`
}
type oauthCodeRecord struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	FamilyID    string    `json:"family_id"`
	ClientID    string    `json:"client_id"`
	ClientName  string    `json:"client_name"`
	RedirectURI string    `json:"redirect_uri"`
	Scope       string    `json:"scope"`
	Audience    string    `json:"audience"`
	Challenge   string    `json:"challenge"`
	ExpiresAt   time.Time `json:"expires_at"`
	Used        bool      `json:"used"`
}
type OAuthConnection struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	FamilyID   string    `json:"family_id"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	Scope      string    `json:"scope"`
	Audience   string    `json:"audience"`
	CreatedAt  time.Time `json:"created_at"`
	Revoked    bool      `json:"revoked"`
}
type oauthTokenRecord struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	GrantID   string    `json:"grant_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}
type OAuthTokens struct {
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshToken     string    `json:"refresh_token"`
	Scope            string    `json:"scope"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}
type ExchangeOAuthCodeParams struct {
	ClientID     string `json:"client_id"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	Resource     string `json:"resource"`
}
type RefreshOAuthTokenParams struct {
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
	Resource     string `json:"resource"`
}
type RevokeOAuthTokenParams struct {
	Token         string `json:"token"`
	TokenTypeHint string `json:"token_type_hint,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}
type RevokeConnectionParams struct {
	ConnectionID string `json:"connection_id"`
	RequestID    string `json:"request_id"`
}

func parseOAuthURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\r\n\t\\") {
		return nil, false
	}
	return u, true
}
func ValidOAuthClientID(raw string) bool {
	u, ok := parseOAuthURL(raw)
	if !ok || u.Scheme != "https" || u.Path == "" || u.RawQuery != "" || len(raw) > 2048 {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}
func validOAuthRedirect(raw string) bool {
	u, ok := parseOAuthURL(raw)
	if !ok || len(raw) > 2048 {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}
func ParseOAuthClientMetadata(id string, data []byte) (OAuthClient, error) {
	var doc struct {
		ID           string          `json:"client_id"`
		Name         string          `json:"client_name"`
		Redirects    []string        `json:"redirect_uris"`
		Method       string          `json:"token_endpoint_auth_method"`
		Methods      json.RawMessage `json:"token_endpoint_auth_methods_supported"`
		Secret       json.RawMessage `json:"client_secret"`
		SecretExpiry json.RawMessage `json:"client_secret_expires_at"`
		Grants       []string        `json:"grant_types"`
		Responses    []string        `json:"response_types"`
	}
	invalid := oauthFailure("invalid_client", "Client metadata is unavailable or invalid")
	if len(data) > 64<<10 || !ValidOAuthClientID(id) || json.Unmarshal(data, &doc) != nil || doc.ID != id || strings.TrimSpace(doc.Name) == "" || len(doc.Name) > 256 || len(doc.Redirects) == 0 || len(doc.Redirects) > 10 || doc.Secret != nil || doc.SecretExpiry != nil {
		return OAuthClient{}, invalid
	}
	if doc.Methods != nil {
		var methods []string
		if json.Unmarshal(doc.Methods, &methods) != nil || !slices.Contains(methods, "none") {
			return OAuthClient{}, invalid
		}
		for _, method := range methods {
			if strings.HasPrefix(method, "client_secret") {
				return OAuthClient{}, invalid
			}
		}
	} else if doc.Method != "" && doc.Method != "none" {
		return OAuthClient{}, invalid
	}
	if strings.HasPrefix(doc.Method, "client_secret") {
		return OAuthClient{}, invalid
	}
	if doc.Grants != nil && !slices.Contains(doc.Grants, "authorization_code") {
		return OAuthClient{}, invalid
	}
	if doc.Responses != nil && !slices.Contains(doc.Responses, "code") {
		return OAuthClient{}, invalid
	}
	for _, uri := range doc.Redirects {
		if !validOAuthRedirect(uri) {
			return OAuthClient{}, invalid
		}
	}
	return OAuthClient{ClientID: id, ClientName: doc.Name, RedirectURIs: doc.Redirects}, nil
}
func (c *Core) resolveOAuthClient(ctx context.Context, id string) (OAuthClient, error) {
	if !ValidOAuthClientID(id) || c.oauthResolver == nil {
		return OAuthClient{}, oauthFailure("invalid_client", "client_id must identify an HTTPS metadata document")
	}
	client, err := c.oauthResolver.Resolve(ctx, id)
	if err != nil || client.ClientID != id {
		return OAuthClient{}, oauthFailure("invalid_client", "Client metadata is unavailable or invalid")
	}
	return client, nil
}
func (c *Core) validateAuthorization(ctx context.Context, p OAuthAuthorizationParams) (OAuthAuthorizationRequest, error) {
	client, err := c.resolveOAuthClient(ctx, p.ClientID)
	if err != nil {
		return OAuthAuthorizationRequest{}, err
	}
	if !validOAuthRedirect(p.RedirectURI) || !slices.Contains(client.RedirectURIs, p.RedirectURI) {
		return OAuthAuthorizationRequest{}, oauthFailure("invalid_redirect_uri", "redirect_uri must exactly match registered client metadata")
	}
	fail := func(code, msg string) (OAuthAuthorizationRequest, error) {
		return OAuthAuthorizationRequest{}, &OAuthError{Code: code, Description: msg, Redirectable: true}
	}
	if p.ResponseType != "code" {
		return fail("unsupported_response_type", "Only response_type=code is supported")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(p.CodeChallenge)
	if err != nil || len(challenge) != sha256.Size || p.CodeChallengeMethod != "S256" {
		return fail("invalid_request", "A valid S256 PKCE challenge is required")
	}
	if len(p.State) < 8 || len(p.State) > 1024 {
		return fail("invalid_request", "state must contain 8 to 1024 characters")
	}
	scope := p.Scope
	if scope == "" {
		scope = "bags:read"
	}
	scopes := strings.Fields(scope)
	for _, s := range scopes {
		if s != "bags:read" && s != "bags:write" && s != "family:write" {
			return fail("invalid_scope", "Requested scope is unsupported")
		}
	}
	if len(scopes) == 0 {
		return fail("invalid_scope", "At least one scope is required")
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	audience := strings.TrimSuffix(c.origin, "/") + "/mcp"
	if p.Resource != "" && p.Resource != audience {
		return fail("invalid_target", "resource must identify this server's /mcp endpoint")
	}
	return OAuthAuthorizationRequest{client, strings.Join(scopes, " "), audience}, nil
}
func (c *Core) createOAuthCode(ctx context.Context, p OAuthAuthorizationParams) (OAuthAuthorizationCodeResult, error) {
	actor, err := c.authorize(ctx, "bags:read")
	if err != nil {
		return OAuthAuthorizationCodeResult{}, err
	}
	if actor.Source != "web" {
		return OAuthAuthorizationCodeResult{}, authDenied()
	}
	request, err := c.validateAuthorization(ctx, p)
	if err != nil {
		return OAuthAuthorizationCodeResult{}, err
	}
	code := "mb_ac_" + c.token()
	record := oauthCodeRecord{ID: hashSecret(code), UserID: actor.UserID, FamilyID: actor.FamilyID, ClientID: p.ClientID, ClientName: request.Client.ClientName, RedirectURI: p.RedirectURI, Scope: request.Scope, Audience: request.Audience, Challenge: p.CodeChallenge, ExpiresAt: c.now().Add(5 * time.Minute)}
	err = c.store.InTx(ctx, "", func(tx Tx) error { return tx.Put("oauth_codes", record.ID, record) })
	return OAuthAuthorizationCodeResult{code, p.RedirectURI, p.State}, err
}
func (c *Core) issueOAuthTokens(tx Tx, grant OAuthConnection) (OAuthTokens, error) {
	if grant.Revoked {
		return OAuthTokens{}, oauthFailure("invalid_grant", "The connection has been revoked")
	}
	access := "mb_at_" + c.token()
	refresh := "mb_rt_" + c.token()
	now := c.now()
	a := oauthTokenRecord{ID: hashSecret(access), UserID: grant.UserID, GrantID: grant.ID, ExpiresAt: now.Add(OAuthAccessTokenLifespan)}
	r := oauthTokenRecord{ID: hashSecret(refresh), UserID: grant.UserID, GrantID: grant.ID, ExpiresAt: now.Add(OAuthRefreshTokenLifespan)}
	if err := tx.Put("oauth_access", a.ID, a); err != nil {
		return OAuthTokens{}, err
	}
	if err := tx.Put("oauth_refresh", r.ID, r); err != nil {
		return OAuthTokens{}, err
	}
	return OAuthTokens{access, "Bearer", int64(OAuthAccessTokenLifespan / time.Second), refresh, grant.Scope, a.ExpiresAt, r.ExpiresAt}, nil
}
func (c *Core) exchangeOAuthCode(ctx context.Context, p ExchangeOAuthCodeParams) (OAuthTokens, error) {
	if p.Code == "" || !pkceVerifierPattern.MatchString(p.CodeVerifier) {
		return OAuthTokens{}, oauthFailure("invalid_request", "code and a valid PKCE verifier are required")
	}
	client, err := c.resolveOAuthClient(ctx, p.ClientID)
	if err != nil {
		return OAuthTokens{}, err
	}
	var record oauthCodeRecord
	err = c.store.InTx(ctx, "", func(tx Tx) error { return tx.Get("oauth_codes", hashSecret(p.Code), &record) })
	if err != nil {
		return OAuthTokens{}, oauthFailure("invalid_grant", "Code is invalid, expired, or used")
	}
	if _, err := c.loadAuthUser(ctx, record.FamilyID, record.UserID); err != nil {
		return OAuthTokens{}, oauthFailure("invalid_grant", "Account is no longer available")
	}
	var out OAuthTokens
	var protocolErr error
	err = c.store.InTx(ctx, "", func(tx Tx) error {
		if err := tx.Get("oauth_codes", hashSecret(p.Code), &record); err != nil {
			return err
		}
		if record.Used || !record.ExpiresAt.After(c.now()) {
			protocolErr = oauthFailure("invalid_grant", "Code is invalid, expired, or used")
			return nil
		}
		record.Used = true
		if err := tx.Put("oauth_codes", record.ID, record); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(p.CodeVerifier))
		challenge := base64.RawURLEncoding.EncodeToString(sum[:])
		if record.ClientID != p.ClientID || record.RedirectURI != p.RedirectURI || !slices.Contains(client.RedirectURIs, p.RedirectURI) || subtle.ConstantTimeCompare([]byte(challenge), []byte(record.Challenge)) != 1 {
			protocolErr = oauthFailure("invalid_grant", "Authorization code validation failed")
			return nil
		}
		if p.Resource != "" && p.Resource != record.Audience {
			protocolErr = oauthFailure("invalid_target", "resource does not match the authorization")
			return nil
		}
		grant := OAuthConnection{ID: c.id(), UserID: record.UserID, FamilyID: record.FamilyID, ClientID: record.ClientID, ClientName: record.ClientName, Scope: record.Scope, Audience: record.Audience, CreatedAt: c.now()}
		if err := tx.Put("oauth_grants", grant.ID, grant); err != nil {
			return err
		}
		var err error
		out, err = c.issueOAuthTokens(tx, grant)
		return err
	})
	if err != nil {
		return out, err
	}
	return out, protocolErr
}
func (c *Core) refreshOAuthToken(ctx context.Context, p RefreshOAuthTokenParams) (OAuthTokens, error) {
	if p.RefreshToken == "" {
		return OAuthTokens{}, oauthFailure("invalid_request", "refresh_token is required")
	}
	if _, err := c.resolveOAuthClient(ctx, p.ClientID); err != nil {
		return OAuthTokens{}, err
	}
	var record oauthTokenRecord
	var grant OAuthConnection
	err := c.store.InTx(ctx, "", func(tx Tx) error {
		if err := tx.Get("oauth_refresh", hashSecret(p.RefreshToken), &record); err != nil {
			return err
		}
		return tx.Get("oauth_grants", record.GrantID, &grant)
	})
	if err != nil {
		return OAuthTokens{}, oauthFailure("invalid_grant", "Refresh token is invalid, expired, or revoked")
	}
	if _, err := c.loadAuthUser(ctx, grant.FamilyID, grant.UserID); err != nil {
		return OAuthTokens{}, oauthFailure("invalid_grant", "Account is no longer available")
	}
	var out OAuthTokens
	var protocolErr error
	err = c.store.InTx(ctx, "", func(tx Tx) error {
		if err := tx.Get("oauth_refresh", hashSecret(p.RefreshToken), &record); err != nil {
			return err
		}
		if err := tx.Get("oauth_grants", record.GrantID, &grant); err != nil {
			return err
		}
		if grant.ClientID != p.ClientID {
			protocolErr = oauthFailure("invalid_grant", "Refresh token validation failed")
			return nil
		}
		if record.Used {
			grant.Revoked = true
			protocolErr = oauthFailure("invalid_grant", "Refresh token is invalid, expired, or revoked")
			return tx.Put("oauth_grants", grant.ID, grant)
		}
		if grant.Revoked || !record.ExpiresAt.After(c.now()) {
			protocolErr = oauthFailure("invalid_grant", "Refresh token is invalid, expired, or revoked")
			return nil
		}
		if p.Resource != "" && p.Resource != grant.Audience {
			protocolErr = oauthFailure("invalid_target", "resource does not match the authorization")
			return nil
		}
		record.Used = true
		if err := tx.Put("oauth_refresh", record.ID, record); err != nil {
			return err
		}
		var err error
		out, err = c.issueOAuthTokens(tx, grant)
		return err
	})
	if err != nil {
		return out, err
	}
	return out, protocolErr
}
func (c *Core) authenticateOAuth(ctx context.Context, token string) (Principal, error) {
	var record oauthTokenRecord
	var grant OAuthConnection
	if token == "" {
		return Principal{}, authDenied()
	}
	err := c.store.InTx(ctx, "", func(tx Tx) error {
		if err := tx.Get("oauth_access", hashSecret(token), &record); err != nil {
			return err
		}
		return tx.Get("oauth_grants", record.GrantID, &grant)
	})
	if err != nil || grant.Revoked || record.Used || !record.ExpiresAt.After(c.now()) || grant.Audience != strings.TrimSuffix(c.origin, "/")+"/mcp" {
		return Principal{}, authDenied()
	}
	p := Principal{UserID: grant.UserID, FamilyID: grant.FamilyID, Scopes: strings.Fields(grant.Scope), Source: "mcp"}
	if _, err := c.loadAuthUser(ctx, p.FamilyID, p.UserID); err != nil {
		return Principal{}, err
	}
	return p, nil
}
func (c *Core) revokeOAuthToken(ctx context.Context, p RevokeOAuthTokenParams) (map[string]bool, error) {
	err := c.store.InTx(ctx, "", func(tx Tx) error {
		for _, kind := range []string{"oauth_refresh", "oauth_access"} {
			var token oauthTokenRecord
			err := tx.Get(kind, hashSecret(p.Token), &token)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			var grant OAuthConnection
			if err := tx.Get("oauth_grants", token.GrantID, &grant); err != nil {
				return err
			}
			if p.ClientID != "" && grant.ClientID != p.ClientID {
				continue
			}
			grant.Revoked = true
			if err := tx.Put("oauth_grants", grant.ID, grant); err != nil {
				return err
			}
		}
		return nil
	})
	return map[string]bool{"revoked": true}, err
}
func (c *Core) registerOAuthActions() {
	Register(c, "prepare_oauth_authorization", ActionInfo{Web: true, Permission: "bags:read"}, c.validateAuthorization)
	Register(c, "create_oauth_authorization_code", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, c.createOAuthCode)
	Register(c, "exchange_oauth_code", ActionInfo{Public: true, Mutation: true}, c.exchangeOAuthCode)
	Register(c, "refresh_oauth_token", ActionInfo{Public: true, Mutation: true}, c.refreshOAuthToken)
	Register(c, "revoke_oauth_token", ActionInfo{Public: true, Mutation: true}, c.revokeOAuthToken)
	Register(c, "authenticate_oauth_token", ActionInfo{Public: true}, func(ctx context.Context, p TokenParams) (Principal, error) { return c.authenticateOAuth(ctx, p.Token) })
	Register(c, "list_connections", ActionInfo{Web: true, MCP: true, Permission: "bags:read"}, func(ctx context.Context, p authListParams) (authPage[OAuthConnection], error) {
		actor, _ := PrincipalFromContext(ctx)
		var grants []OAuthConnection
		err := c.store.InTx(ctx, "", func(tx Tx) error {
			all, err := listAuthRecords[OAuthConnection](tx, "oauth_grants")
			if err != nil {
				return err
			}
			for _, grant := range all {
				if grant.UserID == actor.UserID && !grant.Revoked {
					grants = append(grants, grant)
				}
			}
			return nil
		})
		if err != nil {
			return authPage[OAuthConnection]{}, err
		}
		return authPageOf(grants, p, func(g OAuthConnection) string { return g.ID })
	})
	Register(c, "revoke_connection", ActionInfo{Web: true, MCP: true, Mutation: true, Permission: "bags:read"}, func(ctx context.Context, p RevokeConnectionParams) (map[string]bool, error) {
		actor, _ := PrincipalFromContext(ctx)
		if p.ConnectionID == "" || p.RequestID == "" {
			return nil, authInvalid("connection_id and request_id are required")
		}
		err := c.store.InTx(ctx, "", func(tx Tx) error {
			var grant OAuthConnection
			if err := tx.Get("oauth_grants", p.ConnectionID, &grant); err != nil {
				return err
			}
			if grant.UserID != actor.UserID {
				return ErrNotFound
			}
			grant.Revoked = true
			return tx.Put("oauth_grants", grant.ID, grant)
		})
		return map[string]bool{"revoked": true}, err
	})
}
