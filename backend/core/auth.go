package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const sessionLifetime = 30 * 24 * time.Hour
const recentAuthenticationWindow = 10 * time.Minute

// AuthUser never serializes credentials. Stored credentials are a separate record.
type AuthUser struct {
	ID          string    `json:"id"`
	FamilyID    string    `json:"family_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}
type AuthFamily struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TimeZone  string    `json:"time_zone"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type userCredential struct {
	FamilyID     string `json:"family_id"`
	UserID       string `json:"user_id"`
	PasswordHash []byte `json:"password_hash"`
}
type userDirectory struct {
	ID        string    `json:"id"`
	FamilyID  string    `json:"family_id"`
	Username  string    `json:"username"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}
type authSessionRecord struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	FamilyID        string    `json:"family_id"`
	ExpiresAt       time.Time `json:"expires_at"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
}
type AuthSession struct {
	User      AuthUser   `json:"user"`
	Family    AuthFamily `json:"family"`
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
}
type Identity struct {
	User   AuthUser   `json:"user"`
	Family AuthFamily `json:"family"`
}
type RegisterParams struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	FamilyName  string `json:"family_name"`
	TimeZone    string `json:"time_zone"`
}
type LoginParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type TokenParams struct {
	Token string `json:"token"`
}
type EmptyParams struct{}

type Invitation struct {
	ID         string    `json:"id"`
	FamilyID   string    `json:"family_id"`
	InviterID  string    `json:"inviter_id"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	AcceptedBy string    `json:"accepted_by,omitempty"`
	Revoked    bool      `json:"revoked"`
}
type invitationRoute struct {
	ID        string    `json:"id"`
	FamilyID  string    `json:"family_id"`
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expires_at"`
}
type AcceptInvitationParams struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}
type CreateInvitationParams struct {
	RequestID string `json:"request_id"`
}
type CreateInvitationResult struct {
	Invitation Invitation `json:"invitation"`
	Token      string     `json:"token"`
	URL        string     `json:"url"`
}
type RevokeInvitationParams struct {
	InvitationID string `json:"invitation_id"`
	RequestID    string `json:"request_id"`
}
type DeleteUserParams struct {
	UserID    string `json:"user_id"`
	RequestID string `json:"request_id"`
}
type UpdateFamilyParams struct {
	Name            *string `json:"name,omitempty"`
	TimeZone        *string `json:"time_zone,omitempty"`
	ExpectedVersion int64   `json:"expected_version"`
	RequestID       string  `json:"request_id"`
}
type ChangePasswordParams struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
type ReauthenticateParams struct {
	Password string `json:"password"`
}
type authListParams struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type authPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func authError(code, msg string) error { return &Error{Code: code, Message: msg} }
func authInvalid(msg string) error     { return authError("validation_error", msg) }
func authDenied() error {
	return authError("permission_denied", "Authentication is required or access is no longer available")
}
func hashSecret(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func validateNewAccount(username, password, displayName string) error {
	if len(username) < 3 || len(username) > 80 {
		return authInvalid("username must contain 3 to 80 characters")
	}
	for _, r := range username {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == '@') {
			return authInvalid("username contains unsupported characters")
		}
	}
	if len(password) < 12 || len(password) > 72 {
		return authInvalid("password must contain 12 to 72 bytes")
	}
	if utf8.RuneCountInString(strings.TrimSpace(displayName)) > 100 {
		return authInvalid("display_name must contain at most 100 characters")
	}
	return nil
}

func authPageOf[T any](all []T, p authListParams, id func(T) string) (authPage[T], error) {
	if p.Limit < 0 || p.Limit > 100 {
		return authPage[T]{}, authInvalid("limit must be between 1 and 100")
	}
	if p.Limit == 0 {
		p.Limit = 30
	}
	sort.Slice(all, func(i, j int) bool { return id(all[i]) < id(all[j]) })
	items := make([]T, 0, p.Limit)
	next := ""
	for _, v := range all {
		if id(v) <= p.Cursor {
			continue
		}
		if len(items) == p.Limit {
			next = id(items[len(items)-1])
			break
		}
		items = append(items, v)
	}
	return authPage[T]{items, next}, nil
}
func listAuthRecords[T any](tx Tx, kind string) ([]T, error) {
	rows, err := tx.List(kind)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rows))
	for _, raw := range rows {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Authoritative users live in the family store. A stale global routing record,
// session or OAuth projection can never preserve a deleted account's access.
func (c *Core) loadAuthUser(ctx context.Context, familyID, userID string) (AuthUser, error) {
	var user AuthUser
	if familyID == "" || userID == "" {
		return user, authDenied()
	}
	err := c.store.InTx(ctx, familyID, func(tx Tx) error { return tx.Get("users", userID, &user) })
	if errors.Is(err, ErrNotFound) || user.ID != userID || user.FamilyID != familyID {
		return AuthUser{}, authDenied()
	}
	return user, err
}
func (c *Core) authorize(ctx context.Context, permission string) (Principal, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return p, authDenied()
	}
	if _, err := c.loadAuthUser(ctx, p.FamilyID, p.UserID); err != nil {
		return p, err
	}
	if p.Source == "mcp" {
		allowed := false
		for _, scope := range p.Scopes {
			if scope == permission || permission == "bags:read" && (scope == "bags:write" || scope == "family:write") {
				allowed = true
			}
		}
		if !allowed {
			return p, authError("permission_denied", "The connection does not have the required scope")
		}
	}
	return p, nil
}
func (c *Core) identity(ctx context.Context, p Principal) (Identity, error) {
	var out Identity
	err := c.store.InTx(ctx, p.FamilyID, func(tx Tx) error {
		if err := tx.Get("users", p.UserID, &out.User); err != nil {
			return err
		}
		return tx.Get("families", p.FamilyID, &out.Family)
	})
	return out, err
}
func (c *Core) newAuthSession(ctx context.Context, user AuthUser) (AuthSession, error) {
	var family AuthFamily
	if err := c.store.InTx(ctx, user.FamilyID, func(tx Tx) error {
		var current AuthUser
		if err := tx.Get("users", user.ID, &current); err != nil {
			return err
		}
		return tx.Get("families", user.FamilyID, &family)
	}); err != nil {
		return AuthSession{}, err
	}
	token := c.token()
	now := c.now()
	session := authSessionRecord{hashSecret(token), user.ID, user.FamilyID, now.Add(sessionLifetime), now}
	if err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Put("sessions", hashSecret(token), session) }); err != nil {
		return AuthSession{}, err
	}
	return AuthSession{user, family, token, session.ExpiresAt}, nil
}
func (c *Core) reserveUser(ctx context.Context, user AuthUser) error {
	return c.store.InTx(ctx, "", func(tx Tx) error {
		var exists userDirectory
		err := tx.Get("usernames", user.Username, &exists)
		if err == nil {
			return authError("username_unavailable", "That username is unavailable")
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		record := userDirectory{user.ID, user.FamilyID, user.Username, false, c.now()}
		if err := tx.Put("directory", user.ID, record); err != nil {
			return err
		}
		return tx.Put("usernames", user.Username, record)
	})
}
func (c *Core) activateUser(ctx context.Context, user AuthUser) error {
	return c.store.InTx(ctx, "", func(tx Tx) error {
		var dir userDirectory
		if err := tx.Get("directory", user.ID, &dir); err != nil {
			return err
		}
		dir.Active = true
		if err := tx.Put("directory", user.ID, dir); err != nil {
			return err
		}
		return tx.Put("usernames", user.Username, dir)
	})
}
func (c *Core) releaseReservation(ctx context.Context, user AuthUser) error {
	return c.store.InTx(ctx, "", func(tx Tx) error {
		var dir userDirectory
		if err := tx.Get("directory", user.ID, &dir); err != nil {
			return err
		}
		if dir.Active {
			return nil
		}
		if err := tx.Delete("directory", user.ID); err != nil {
			return err
		}
		return tx.Delete("usernames", user.Username)
	})
}
func (c *Core) registerAccount(ctx context.Context, p RegisterParams) (AuthSession, error) {
	p.Username = normalizeUsername(p.Username)
	if err := validateNewAccount(p.Username, p.Password, p.DisplayName); err != nil {
		return AuthSession{}, err
	}
	p.FamilyName = strings.TrimSpace(p.FamilyName)
	if p.FamilyName == "" {
		p.FamilyName = "My family"
	}
	if utf8.RuneCountInString(p.FamilyName) > 100 {
		return AuthSession{}, authInvalid("family_name is too long")
	}
	if p.TimeZone == "" {
		p.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(p.TimeZone); err != nil {
		return AuthSession{}, authInvalid("time_zone must be an IANA time zone")
	}
	password, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthSession{}, err
	}
	now := c.now()
	family := AuthFamily{c.id(), p.FamilyName, p.TimeZone, 1, now, now}
	user := AuthUser{c.id(), family.ID, p.Username, strings.TrimSpace(p.DisplayName), now}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if err := c.reserveUser(ctx, user); err != nil {
		return AuthSession{}, err
	}
	if provisioner, ok := c.store.(FamilyProvisioner); ok {
		if err := provisioner.CreateFamily(ctx, family.ID); err != nil {
			_ = c.releaseReservation(ctx, user)
			return AuthSession{}, err
		}
	}
	err = c.store.InTx(ctx, family.ID, func(tx Tx) error {
		if err := tx.Put("families", family.ID, family); err != nil {
			return err
		}
		if err := tx.Put("users", user.ID, user); err != nil {
			return err
		}
		return tx.Put("credentials", user.ID, userCredential{user.FamilyID, user.ID, password})
	})
	if err != nil {
		_ = c.releaseReservation(ctx, user)
		return AuthSession{}, err
	}
	if err := c.activateUser(ctx, user); err != nil {
		return AuthSession{}, err
	}
	return c.newAuthSession(ctx, user)
}

// A valid bcrypt digest makes unknown-user checks comparable to known-user checks.
var dummyPasswordHash = []byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5uZsWdT99gz65dQySYEcXNKNLSMjH7q")

func (c *Core) login(ctx context.Context, p LoginParams) (AuthSession, error) {
	var dir userDirectory
	err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Get("usernames", normalizeUsername(p.Username), &dir) })
	if err != nil || !dir.Active {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(p.Password))
		return AuthSession{}, authError("invalid_credentials", "Username or password is incorrect")
	}
	var user AuthUser
	var credential userCredential
	err = c.store.InTx(ctx, dir.FamilyID, func(tx Tx) error {
		if err := tx.Get("users", dir.ID, &user); err != nil {
			return err
		}
		return tx.Get("credentials", dir.ID, &credential)
	})
	if err != nil || bcrypt.CompareHashAndPassword(credential.PasswordHash, []byte(p.Password)) != nil {
		return AuthSession{}, authError("invalid_credentials", "Username or password is incorrect")
	}
	return c.newAuthSession(ctx, user)
}
func (c *Core) authenticateSession(ctx context.Context, token string) (Principal, error) {
	var record authSessionRecord
	if token == "" {
		return Principal{}, authDenied()
	}
	err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Get("sessions", hashSecret(token), &record) })
	if err != nil || !record.ExpiresAt.After(c.now()) {
		return Principal{}, authDenied()
	}
	if _, err := c.loadAuthUser(ctx, record.FamilyID, record.UserID); err != nil {
		return Principal{}, err
	}
	return Principal{UserID: record.UserID, FamilyID: record.FamilyID, Source: "web", SessionID: hashSecret(token), AuthenticatedAt: record.AuthenticatedAt}, nil
}

func (c *Core) createInvitation(ctx context.Context, p CreateInvitationParams) (CreateInvitationResult, error) {
	if strings.TrimSpace(p.RequestID) == "" || len(p.RequestID) > 200 {
		return CreateInvitationResult{}, authInvalid("request_id must contain 1 to 200 characters")
	}
	principal, err := c.authorize(ctx, "family:write")
	if err != nil {
		return CreateInvitationResult{}, err
	}
	token := c.token()
	now := c.now()
	inv := Invitation{ID: c.id(), FamilyID: principal.FamilyID, InviterID: principal.UserID, ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now}
	out := CreateInvitationResult{}
	created := false
	// Publish only an opaque hash in global routing. Orphan routes grant nothing:
	// acceptance always checks the authoritative family invitation transaction.
	if err := c.store.InTx(ctx, "", func(tx Tx) error {
		return tx.Put("invitation_routes", hashSecret(token), invitationRoute{ID: inv.ID, FamilyID: inv.FamilyID, Hash: hashSecret(token), ExpiresAt: inv.ExpiresAt})
	}); err != nil {
		return out, err
	}
	err = c.authMutation(ctx, principal, "create_invitation", p.RequestID, p, func(tx Tx) (any, error) {
		created = true
		if err := tx.Put("invitations", inv.ID, inv); err != nil {
			return nil, err
		}
		return CreateInvitationResult{Invitation: inv}, nil
	}, &out)
	if err != nil || !created {
		_ = c.store.InTx(ctx, "", func(tx Tx) error { return tx.Delete("invitation_routes", hashSecret(token)) })
	}
	if err != nil {
		return out, err
	}
	if created {
		out.Token = token
		out.URL = strings.TrimSuffix(c.origin, "/") + "/invite?token=" + token
	}
	return out, nil
}
func (c *Core) acceptInvitation(ctx context.Context, p AcceptInvitationParams) (AuthSession, error) {
	p.Username = normalizeUsername(p.Username)
	if err := validateNewAccount(p.Username, p.Password, p.DisplayName); err != nil {
		return AuthSession{}, err
	}
	var route invitationRoute
	if p.Token == "" {
		return AuthSession{}, authInvalid("Invitation is invalid or expired")
	}
	if err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Get("invitation_routes", hashSecret(p.Token), &route) }); err != nil {
		return AuthSession{}, authInvalid("Invitation is invalid or expired")
	}
	password, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthSession{}, err
	}
	user := AuthUser{c.id(), route.FamilyID, p.Username, strings.TrimSpace(p.DisplayName), c.now()}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if err := c.reserveUser(ctx, user); err != nil {
		return AuthSession{}, err
	}
	err = c.store.InTx(ctx, route.FamilyID, func(tx Tx) error {
		var inv Invitation
		if err := tx.Get("invitations", route.ID, &inv); err != nil {
			return authInvalid("Invitation is invalid or expired")
		}
		if inv.Revoked || inv.AcceptedBy != "" || !inv.ExpiresAt.After(c.now()) {
			return authInvalid("Invitation is invalid or expired")
		}
		inv.AcceptedBy = user.ID
		if err := tx.Put("users", user.ID, user); err != nil {
			return err
		}
		if err := tx.Put("credentials", user.ID, userCredential{user.FamilyID, user.ID, password}); err != nil {
			return err
		}
		return tx.Put("invitations", inv.ID, inv)
	})
	if err != nil {
		_ = c.releaseReservation(ctx, user)
		return AuthSession{}, err
	}
	if err := c.activateUser(ctx, user); err != nil {
		return AuthSession{}, err
	}
	return c.newAuthSession(ctx, user)
}

type authIdempotency struct {
	FamilyID string          `json:"family_id"`
	Hash     string          `json:"hash"`
	Result   json.RawMessage `json:"result"`
}

func (c *Core) authMutation(ctx context.Context, p Principal, action, key string, input any, fn func(Tx) (any, error), out any) error {
	if strings.TrimSpace(key) == "" || len(key) > 200 {
		return authInvalid("request_id must contain 1 to 200 characters")
	}
	raw, _ := json.Marshal(input)
	fingerprint := hashSecret(string(raw))
	id := hashSecret(p.UserID + "\x00" + action + "\x00" + key)
	return c.store.InTx(ctx, p.FamilyID, func(tx Tx) error {
		var active AuthUser
		if err := tx.Get("users", p.UserID, &active); err != nil {
			return authDenied()
		}
		var previous authIdempotency
		err := tx.Get("auth_idempotency", id, &previous)
		if err == nil {
			if previous.Hash != fingerprint {
				return authError("idempotency_conflict", "request_id was already used with different parameters")
			}
			return json.Unmarshal(previous.Result, out)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		result, err := fn(tx)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := tx.Put("auth_idempotency", id, authIdempotency{p.FamilyID, fingerprint, encoded}); err != nil {
			return err
		}
		audit := map[string]any{"id": c.id(), "family_id": p.FamilyID, "actor_id": p.UserID, "action": action, "source": p.Source, "request_id": key, "created_at": c.now()}
		if err := tx.Put("audit", audit["id"].(string), audit); err != nil {
			return err
		}
		return json.Unmarshal(encoded, out)
	})
}
func (c *Core) deleteUser(ctx context.Context, p DeleteUserParams) (map[string]bool, error) {
	actor, err := c.authorize(ctx, "family:write")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	var removed AuthUser
	err = c.authMutation(ctx, actor, "delete_user", p.RequestID, p, func(tx Tx) (any, error) {
		if err := tx.Get("users", p.UserID, &removed); err != nil {
			return nil, err
		}
		if removed.FamilyID != actor.FamilyID {
			return nil, ErrNotFound
		}
		keys, err := listAuthRecords[passkeyRecord](tx, "passkeys")
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if key.UserID == p.UserID {
				if err := tx.Delete("passkeys", key.ID); err != nil {
					return nil, err
				}
			}
		}
		if err := tx.Delete("credentials", p.UserID); err != nil {
			return nil, err
		}
		if err := tx.Delete("bag_preferences", p.UserID); err != nil {
			return nil, err
		}
		if err := tx.Delete("users", p.UserID); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	}, &out)
	if err != nil {
		return nil, err
	}
	// A failed projection cleanup cannot reauthorize this account. Reconciliation
	// can remove stale global records because the authoritative deletion committed.
	err = c.purgeDeletedUser(ctx, p.UserID)
	return out, err
}
func (c *Core) purgeDeletedUser(ctx context.Context, userID string) error {
	return c.store.InTx(ctx, "", func(tx Tx) error {
		var dir userDirectory
		if err := tx.Get("directory", userID, &dir); err == nil {
			var named userDirectory
			if err := tx.Get("usernames", dir.Username, &named); err == nil && named.ID == userID {
				if err := tx.Delete("usernames", dir.Username); err != nil {
					return err
				}
			}
			if err := tx.Delete("directory", userID); err != nil {
				return err
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, kind := range []string{"sessions", "oauth_codes", "oauth_grants", "oauth_access", "oauth_refresh", "passkey_challenges"} {
			rows, err := tx.List(kind)
			if err != nil {
				return err
			}
			for _, raw := range rows {
				var record struct {
					ID     string `json:"id"`
					UserID string `json:"user_id"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					return err
				}
				if record.UserID != userID {
					continue
				}
				if record.ID != "" {
					if err := tx.Delete(kind, record.ID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
func (c *Core) reauthenticate(ctx context.Context, password string) (Principal, error) {
	p, err := c.authorize(ctx, "bags:read")
	if err != nil {
		return p, err
	}
	if p.Source != "web" || p.SessionID == "" {
		return p, authError("recent_authentication_required", "Continue in the browser to authenticate again")
	}
	var cred userCredential
	err = c.store.InTx(ctx, p.FamilyID, func(tx Tx) error { return tx.Get("credentials", p.UserID, &cred) })
	if err != nil || bcrypt.CompareHashAndPassword(cred.PasswordHash, []byte(password)) != nil {
		return p, authError("invalid_credentials", "Password is incorrect")
	}
	p.AuthenticatedAt = c.now()
	err = c.store.InTx(ctx, "", func(tx Tx) error {
		var session authSessionRecord
		if err := tx.Get("sessions", p.SessionID, &session); err != nil {
			return err
		}
		session.AuthenticatedAt = p.AuthenticatedAt
		return tx.Put("sessions", p.SessionID, session)
	})
	return p, err
}
func (c *Core) requireRecent(ctx context.Context) (Principal, error) {
	p, err := c.authorize(ctx, "bags:read")
	if err != nil {
		return p, err
	}
	if p.Source != "web" || p.SessionID == "" || p.AuthenticatedAt.IsZero() || c.now().Sub(p.AuthenticatedAt) > recentAuthenticationWindow {
		return p, authError("recent_authentication_required", "Authenticate again in the browser before changing credentials")
	}
	return p, nil
}
func (c *Core) changePassword(ctx context.Context, p ChangePasswordParams) (map[string]bool, error) {
	principal, err := c.reauthenticate(ctx, p.CurrentPassword)
	if err != nil {
		return nil, err
	}
	if len(p.NewPassword) < 12 || len(p.NewPassword) > 72 {
		return nil, authInvalid("new_password must contain 12 to 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	err = c.store.InTx(ctx, principal.FamilyID, func(tx Tx) error {
		var user AuthUser
		if err := tx.Get("users", principal.UserID, &user); err != nil {
			return authDenied()
		}
		return tx.Put("credentials", principal.UserID, userCredential{principal.FamilyID, principal.UserID, hash})
	})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"changed": true}, nil
}

func (c *Core) registerAuthActions() {
	public := ActionInfo{Web: true, Public: true, Mutation: true}
	productRead := ActionInfo{Web: true, MCP: true, Permission: "bags:read"}
	productWrite := ActionInfo{Web: true, MCP: true, Mutation: true, Permission: "family:write"}
	Register(c, "register", public, c.registerAccount)
	Register(c, "login", public, c.login)
	Register(c, "accept_invitation", public, c.acceptInvitation)
	Register(c, "authenticate_session", ActionInfo{Public: true}, func(ctx context.Context, p TokenParams) (Principal, error) {
		return c.authenticateSession(ctx, p.Token)
	})
	Register(c, "whoami", productRead, func(ctx context.Context, _ EmptyParams) (Identity, error) {
		p, err := c.authorize(ctx, "bags:read")
		if err != nil {
			return Identity{}, err
		}
		return c.identity(ctx, p)
	})
	Register(c, "get_family", productRead, func(ctx context.Context, _ EmptyParams) (AuthFamily, error) {
		p, err := c.authorize(ctx, "bags:read")
		if err != nil {
			return AuthFamily{}, err
		}
		identity, err := c.identity(ctx, p)
		return identity.Family, err
	})
	Register(c, "logout", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, func(ctx context.Context, _ EmptyParams) (map[string]bool, error) {
		p, _ := PrincipalFromContext(ctx)
		if p.SessionID == "" {
			return nil, authDenied()
		}
		err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Delete("sessions", p.SessionID) })
		return map[string]bool{"logged_out": true}, err
	})
	Register(c, "reauthenticate", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, func(ctx context.Context, p ReauthenticateParams) (map[string]bool, error) {
		_, err := c.reauthenticate(ctx, p.Password)
		return map[string]bool{"authenticated": true}, err
	})
	Register(c, "change_password", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, c.changePassword)
	Register(c, "list_users", productRead, func(ctx context.Context, p authListParams) (authPage[AuthUser], error) {
		actor, _ := PrincipalFromContext(ctx)
		var users []AuthUser
		err := c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error { var err error; users, err = listAuthRecords[AuthUser](tx, "users"); return err })
		if err != nil {
			return authPage[AuthUser]{}, err
		}
		return authPageOf(users, p, func(u AuthUser) string { return u.ID })
	})
	Register(c, "delete_user", productWrite, c.deleteUser)
	Register(c, "update_family", productWrite, func(ctx context.Context, p UpdateFamilyParams) (AuthFamily, error) {
		actor, _ := PrincipalFromContext(ctx)
		var out AuthFamily
		err := c.authMutation(ctx, actor, "update_family", p.RequestID, p, func(tx Tx) (any, error) {
			var family AuthFamily
			if err := tx.Get("families", actor.FamilyID, &family); err != nil {
				return nil, err
			}
			if family.Version != p.ExpectedVersion {
				return nil, &Error{Code: "version_conflict", Message: "The family was changed by another request", Current: family}
			}
			if p.Name != nil {
				family.Name = strings.TrimSpace(*p.Name)
				if family.Name == "" || utf8.RuneCountInString(family.Name) > 100 {
					return nil, authInvalid("name must contain 1 to 100 characters")
				}
			}
			if p.TimeZone != nil {
				if _, err := time.LoadLocation(*p.TimeZone); err != nil {
					return nil, authInvalid("time_zone must be an IANA time zone")
				}
				family.TimeZone = *p.TimeZone
			}
			family.Version++
			family.UpdatedAt = c.now()
			return family, tx.Put("families", family.ID, family)
		}, &out)
		return out, err
	})
	Register(c, "create_invitation", productWrite, c.createInvitation)
	Register(c, "list_invitations", productRead, func(ctx context.Context, p authListParams) (authPage[Invitation], error) {
		actor, _ := PrincipalFromContext(ctx)
		var invs []Invitation
		err := c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error {
			var err error
			invs, err = listAuthRecords[Invitation](tx, "invitations")
			return err
		})
		if err != nil {
			return authPage[Invitation]{}, err
		}
		return authPageOf(invs, p, func(i Invitation) string { return i.ID })
	})
	Register(c, "revoke_invitation", productWrite, func(ctx context.Context, p RevokeInvitationParams) (map[string]bool, error) {
		actor, _ := PrincipalFromContext(ctx)
		out := map[string]bool{}
		err := c.authMutation(ctx, actor, "revoke_invitation", p.RequestID, p, func(tx Tx) (any, error) {
			var inv Invitation
			if err := tx.Get("invitations", p.InvitationID, &inv); err != nil {
				return nil, err
			}
			inv.Revoked = true
			return map[string]bool{"revoked": true}, tx.Put("invitations", inv.ID, inv)
		}, &out)
		return out, err
	})
	c.registerPasskeyActions()
	c.registerOAuthActions()
	c.registerAuthReconciliation()
}

// Reconciliation removes stale routing/projection rows but never publishes a
// pending family. Operators receive pending and unreachable records for review.
func (c *Core) registerAuthReconciliation() {
	Register(c, "reconcile_auth", ActionInfo{Administrative: true, Mutation: true}, func(ctx context.Context, _ EmptyParams) (map[string]any, error) {
		var dirs []userDirectory
		if err := c.store.InTx(ctx, "", func(tx Tx) error {
			var err error
			dirs, err = listAuthRecords[userDirectory](tx, "directory")
			return err
		}); err != nil {
			return nil, err
		}
		removed, pending, unreachable, orphans := []string{}, []string{}, []string{}, []string{}
		var available map[string]bool
		if lister, ok := c.store.(FamilyLister); ok {
			ids, err := lister.FamilyIDs(ctx)
			if err != nil {
				return nil, err
			}
			available = map[string]bool{}
			for _, id := range ids {
				available[id] = true
			}
		}
		routed := map[string]bool{}
		for _, dir := range dirs {
			routed[dir.FamilyID] = true
			if available != nil && !available[dir.FamilyID] {
				unreachable = append(unreachable, dir.FamilyID)
				continue
			}
			var user AuthUser
			err := c.store.InTx(ctx, dir.FamilyID, func(tx Tx) error { return tx.Get("users", dir.ID, &user) })
			if errors.Is(err, ErrNotFound) {
				if err := c.purgeDeletedUser(ctx, dir.ID); err != nil {
					return nil, err
				}
				removed = append(removed, dir.ID)
			} else if err != nil {
				unreachable = append(unreachable, dir.FamilyID)
			} else if !dir.Active {
				pending = append(pending, dir.ID)
			}
		}
		for id := range available {
			if routed[id] {
				continue
			}
			var family AuthFamily
			err := c.store.InTx(ctx, id, func(tx Tx) error { return tx.Get("families", id, &family) })
			if err != nil && !errors.Is(err, ErrNotFound) {
				unreachable = append(unreachable, id)
			} else {
				orphans = append(orphans, id)
			}
		}
		sort.Strings(removed)
		sort.Strings(pending)
		sort.Strings(unreachable)
		sort.Strings(orphans)
		return map[string]any{"removed_user_ids": removed, "pending_user_ids": pending, "unreachable_family_ids": unreachable, "unrouted_family_ids": orphans}, nil
	})
	Register(c, "cleanup_auth", ActionInfo{Administrative: true, Mutation: true}, func(ctx context.Context, _ EmptyParams) (map[string]any, error) {
		removed := 0
		err := c.store.InTx(ctx, "", func(tx Tx) error {
			for _, kind := range []string{"sessions", "passkey_challenges", "oauth_codes", "oauth_access", "oauth_refresh", "invitation_routes"} {
				rows, err := tx.List(kind)
				if err != nil {
					return err
				}
				for _, raw := range rows {
					var record struct {
						ID        string    `json:"id"`
						Hash      string    `json:"hash"`
						GrantID   string    `json:"grant_id"`
						Used      bool      `json:"used"`
						ExpiresAt time.Time `json:"expires_at"`
					}
					if err := json.Unmarshal(raw, &record); err != nil {
						return err
					}
					if kind == "invitation_routes" {
						record.ID = record.Hash
					}
					// Keep rotated refresh tombstones while their grant is live;
					// old-token replay must still revoke newer descendants.
					if kind == "oauth_refresh" && record.Used {
						var grant OAuthConnection
						err := tx.Get("oauth_grants", record.GrantID, &grant)
						if err == nil && !grant.Revoked {
							continue
						}
						if err != nil && !errors.Is(err, ErrNotFound) {
							return err
						}
					}
					if record.ID != "" && !record.ExpiresAt.After(c.now()) {
						if err := tx.Delete(kind, record.ID); err != nil {
							return err
						}
						removed++
					}
				}
			}
			return nil
		})
		return map[string]any{"removed_records": removed}, err
	})
}
