package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/memstore"
)

const testPassword = "correct horse battery staple"

type authHarness struct {
	c        *core.Core
	store    *memstore.Store
	clock    *time.Time
	resolver *authResolver
}
type authResolver struct{ client core.OAuthClient }

func (r *authResolver) Resolve(_ context.Context, id string) (core.OAuthClient, error) {
	if id != r.client.ClientID {
		return core.OAuthClient{}, errors.New("unknown client")
	}
	return r.client, nil
}
func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	store := memstore.New()
	now := time.Now().UTC()
	resolver := &authResolver{core.OAuthClient{ClientID: "https://client.example/metadata.json", ClientName: "Test assistant", RedirectURIs: []string{"https://client.example/callback"}}}
	c := core.New(core.Config{AllowRegistration: true, Store: store, Clock: func() time.Time { return now }, Origin: "https://money.example", RPID: "money.example", OAuthResolver: resolver})
	return &authHarness{c, store, &now, resolver}
}
func authCall[T any](t *testing.T, c *core.Core, ctx context.Context, name string, params any) T {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.InvokeJSON(ctx, name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var result T
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func authCallError(t *testing.T, c *core.Core, ctx context.Context, name string, params any) error {
	t.Helper()
	raw, _ := json.Marshal(params)
	_, err := c.InvokeJSON(ctx, name, raw)
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", name)
	}
	return err
}
func (h *authHarness) register(t *testing.T, username string) (core.AuthSession, context.Context) {
	t.Helper()
	s := authCall[core.AuthSession](t, h.c, context.Background(), "register", core.RegisterParams{Username: username, Password: testPassword, DisplayName: "Person " + username, FamilyName: "Family " + username, TimeZone: "America/Chicago"})
	return s, h.sessionContext(t, s.Token)
}
func (h *authHarness) sessionContext(t *testing.T, token string) context.Context {
	t.Helper()
	p := authCall[core.Principal](t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: token})
	return core.WithPrincipal(context.Background(), p)
}
func requireAuthCode(t *testing.T, err error, code string) {
	t.Helper()
	var app *core.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
}
func requireOAuthCode(t *testing.T, err error, code string) {
	t.Helper()
	var app *core.OAuthError
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("wanted OAuth %s, got %v", code, err)
	}
}

func TestRegistrationDisabledByDefault(t *testing.T) {
	// No store is needed: the gate must reject signup before any storage access.
	c := core.New(core.Config{})
	requireAuthCode(t, authCallError(t, c, context.Background(), "register", core.RegisterParams{Username: "alice", Password: testPassword}), "permission_denied")
}

func TestDisablingRegistrationPreservesLoginAndInvitations(t *testing.T) {
	h := newAuthHarness(t)
	alice, _ := h.register(t, "alice")
	// Reopening the same store models disabling registration and restarting.
	h.c = core.New(core.Config{Store: h.store, Clock: func() time.Time { return *h.clock }})
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "register", core.RegisterParams{Username: "bob", Password: testPassword}), "permission_denied")
	loggedIn := authCall[core.AuthSession](t, h.c, context.Background(), "login", core.LoginParams{Username: "alice", Password: testPassword})
	ctx := h.sessionContext(t, loggedIn.Token)
	invite := authCall[core.CreateInvitationResult](t, h.c, ctx, "create_invitation", core.CreateInvitationParams{RequestID: "join"})
	bob := authCall[core.AuthSession](t, h.c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: invite.Token, Username: "bob", Password: testPassword})
	if bob.Family.ID != alice.Family.ID {
		t.Fatal("invited user did not join the existing family")
	}
}

func TestAuthenticationRegistrationAndHashedSession(t *testing.T) {
	h := newAuthHarness(t)
	session, ctx := h.register(t, "Alice")
	if session.User.Username != "alice" {
		t.Fatal("username not normalized")
	}
	authCall[core.Identity](t, h.c, ctx, "whoami", map[string]any{})
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "register", core.RegisterParams{Username: "ALICE", Password: testPassword}), "username_unavailable")
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "login", core.LoginParams{Username: "alice", Password: "wrong password"}), "invalid_credentials")
	second := authCall[core.AuthSession](t, h.c, context.Background(), "login", core.LoginParams{Username: " ALICE ", Password: testPassword})
	if second.Token == session.Token {
		t.Fatal("sessions must use independent secrets")
	}
	if err := h.store.InTx(ctx, "", func(tx core.Tx) error {
		rows, err := tx.List("sessions")
		if err != nil {
			return err
		}
		for _, raw := range rows {
			if strings.Contains(string(raw), session.Token) || strings.Contains(string(raw), testPassword) {
				t.Fatal("plaintext secret stored")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	authCall[map[string]bool](t, h.c, ctx, "logout", map[string]any{})
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: session.Token}), "permission_denied")
	*h.clock = h.clock.Add(31 * 24 * time.Hour)
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: second.Token}), "permission_denied")
}

func TestInvitationsSingleUseExpiryAndEqualFamilyRights(t *testing.T) {
	h := newAuthHarness(t)
	alice, ctx := h.register(t, "alice")
	invite := authCall[core.CreateInvitationResult](t, h.c, ctx, "create_invitation", core.CreateInvitationParams{RequestID: "invite-bob"})
	if invite.Token == "" || invite.URL == "" {
		t.Fatal("one-time invitation was not returned")
	}
	replay := authCall[core.CreateInvitationResult](t, h.c, ctx, "create_invitation", core.CreateInvitationParams{RequestID: "invite-bob"})
	if replay.Invitation.ID != invite.Invitation.ID || replay.Token != "" || replay.URL != "" {
		t.Fatal("invitation retry duplicated or revealed a stored secret")
	}
	bob := authCall[core.AuthSession](t, h.c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: invite.Token, Username: "bob", Password: testPassword})
	if bob.Family.ID != alice.Family.ID {
		t.Fatal("invitation changed tenant")
	}
	authCallError(t, h.c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: invite.Token, Username: "charlie", Password: testPassword})
	bobctx := h.sessionContext(t, bob.Token)
	name := "Changed by invited person"
	updated := authCall[core.AuthFamily](t, h.c, bobctx, "update_family", core.UpdateFamilyParams{RequestID: "rename", ExpectedVersion: 1, Name: &name})
	if updated.Name != name {
		t.Fatal("invited user lacks equal permissions")
	}
	expired := authCall[core.CreateInvitationResult](t, h.c, bobctx, "create_invitation", core.CreateInvitationParams{RequestID: "expiry"})
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	authCallError(t, h.c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: expired.Token, Username: "charlie", Password: testPassword})
	var rows []json.RawMessage
	if err := h.store.InTx(ctx, alice.Family.ID, func(tx core.Tx) error { var err error; rows, err = tx.List("auth_idempotency"); return err }); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(string(row), invite.Token) {
			t.Fatal("invitation secret persisted")
		}
	}
}

func TestInvitationConcurrentAcceptance(t *testing.T) {
	h := newAuthHarness(t)
	_, ctx := h.register(t, "alice")
	invite := authCall[core.CreateInvitationResult](t, h.c, ctx, "create_invitation", core.CreateInvitationParams{RequestID: "race"})
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for _, name := range []string{"racer-one", "racer-two"} {
		wg.Go(func() {
			raw, _ := json.Marshal(core.AcceptInvitationParams{Token: invite.Token, Username: name, Password: testPassword})
			_, err := h.c.InvokeJSON(context.Background(), "accept_invitation", raw)
			success <- err == nil
		})
	}
	wg.Wait()
	close(success)
	count := 0
	for ok := range success {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d accounts accepted one invitation", count)
	}
}

func TestDeleteAccountRevokesAndPreservesHistoricalAuthors(t *testing.T) {
	h := newAuthHarness(t)
	alice, actx := h.register(t, "alice")
	invite := authCall[core.CreateInvitationResult](t, h.c, actx, "create_invitation", core.CreateInvitationParams{RequestID: "join"})
	bob := authCall[core.AuthSession](t, h.c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: invite.Token, Username: "bob", Password: testPassword, DisplayName: "Bob"})
	bctx := h.sessionContext(t, bob.Token)
	bag := authCall[core.BagResult](t, h.c, bctx, "create_bag", core.CreateBagParams{RequestID: "bag", Name: "Food"})
	amount := int64(-1200)
	entry := authCall[core.EntryResult](t, h.c, bctx, "create_entry", core.CreateEntryParams{RequestID: "spend", BagID: bag.ID, AmountCents: &amount})
	other, otherctx := h.register(t, "other-family")
	requireAuthCode(t, authCallError(t, h.c, otherctx, "delete_user", core.DeleteUserParams{RequestID: "guess", UserID: bob.User.ID}), "not_found")
	_ = other
	authCall[map[string]bool](t, h.c, actx, "delete_user", core.DeleteUserParams{RequestID: "delete-bob", UserID: bob.User.ID})
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: bob.Token}), "permission_denied")
	authCallError(t, h.c, context.Background(), "login", core.LoginParams{Username: "bob", Password: testPassword})
	saved := authCall[core.EntryResult](t, h.c, actx, "get_entry", core.GetEntryParams{EntryID: entry.ID})
	if saved.AuthorID != bob.User.ID || saved.AuthorName != "Bob" || saved.BalanceCents != -1200 {
		t.Fatal("account deletion altered financial history")
	}
	if err := h.store.InTx(context.Background(), "", func(tx core.Tx) error {
		rows, _ := tx.List("sessions")
		for _, row := range rows {
			if strings.Contains(string(row), bob.User.ID) {
				t.Fatal("deleted user session was not removed")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	authCall[map[string]bool](t, h.c, actx, "delete_user", core.DeleteUserParams{RequestID: "self", UserID: alice.User.ID})
	authCallError(t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: alice.Token})
}

func TestPasswordChangeRequiresCurrentPasswordAndBrowserStepUp(t *testing.T) {
	h := newAuthHarness(t)
	session, ctx := h.register(t, "alice")
	*h.clock = h.clock.Add(11 * time.Minute)
	authCallError(t, h.c, ctx, "begin_passkey_registration", map[string]any{})
	authCallError(t, h.c, ctx, "change_password", core.ChangePasswordParams{CurrentPassword: "wrong", NewPassword: "new secure password here"})
	authCall[map[string]bool](t, h.c, ctx, "change_password", core.ChangePasswordParams{CurrentPassword: testPassword, NewPassword: "new secure password here"})
	authCallError(t, h.c, context.Background(), "login", core.LoginParams{Username: "alice", Password: testPassword})
	authCall[core.AuthSession](t, h.c, context.Background(), "login", core.LoginParams{Username: "alice", Password: "new secure password here"})
	fresh := h.sessionContext(t, session.Token)
	authCall[core.PasskeyOptions](t, h.c, fresh, "begin_passkey_registration", map[string]any{})
	p := authCall[core.Principal](t, h.c, context.Background(), "authenticate_session", core.TokenParams{Token: session.Token})
	p.Source = "mcp"
	p.Scopes = []string{"bags:write"}
	mcp := core.WithPrincipal(context.Background(), p)
	requireAuthCode(t, authCallError(t, h.c, mcp, "change_password", core.ChangePasswordParams{CurrentPassword: "new secure password here", NewPassword: testPassword}), "recent_authentication_required")
}

func oauthParams(h *authHarness, scope string) (core.OAuthAuthorizationParams, string) {
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	return core.OAuthAuthorizationParams{ResponseType: "code", ClientID: h.resolver.client.ClientID, RedirectURI: h.resolver.client.RedirectURIs[0], Scope: scope, State: "opaque-csrf-state", CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), CodeChallengeMethod: "S256", Resource: "https://money.example/mcp"}, verifier
}
func (h *authHarness) issueOAuth(t *testing.T, ctx context.Context, scope string) core.OAuthTokens {
	t.Helper()
	p, verifier := oauthParams(h, scope)
	authCall[core.OAuthAuthorizationRequest](t, h.c, ctx, "prepare_oauth_authorization", p)
	code := authCall[core.OAuthAuthorizationCodeResult](t, h.c, ctx, "create_oauth_authorization_code", p)
	return authCall[core.OAuthTokens](t, h.c, context.Background(), "exchange_oauth_code", core.ExchangeOAuthCodeParams{ClientID: p.ClientID, Code: code.Code, RedirectURI: p.RedirectURI, CodeVerifier: verifier, Resource: p.Resource})
}
func TestOAuthScopeAndDurableRefreshReplayRevocation(t *testing.T) {
	h := newAuthHarness(t)
	_, ctx := h.register(t, "alice")
	tokens := h.issueOAuth(t, ctx, "bags:read")
	principal := authCall[core.Principal](t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: tokens.AccessToken})
	mcpctx := core.WithPrincipal(context.Background(), principal)
	authCall[core.Identity](t, h.c, mcpctx, "whoami", map[string]any{})
	requireAuthCode(t, authCallError(t, h.c, mcpctx, "create_bag", core.CreateBagParams{RequestID: "scope", Name: "Food"}), "permission_denied")
	next := authCall[core.OAuthTokens](t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: tokens.RefreshToken})
	authCall[core.Principal](t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: next.AccessToken})
	requireOAuthCode(t, authCallError(t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: tokens.RefreshToken}), "invalid_grant")
	authCallError(t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: tokens.AccessToken})
	authCallError(t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: next.AccessToken})
	requireOAuthCode(t, authCallError(t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: next.RefreshToken}), "invalid_grant")
}
func TestOAuthPKCEAudienceCodeSingleUseAndDeletion(t *testing.T) {
	h := newAuthHarness(t)
	session, ctx := h.register(t, "alice")
	p, verifier := oauthParams(h, "bags:write family:write")
	bad := p
	bad.RedirectURI += "/guess"
	err := authCallError(t, h.c, ctx, "prepare_oauth_authorization", bad)
	var oe *core.OAuthError
	if !errors.As(err, &oe) || oe.Redirectable {
		t.Fatal("untrusted callback error may not redirect")
	}
	bad = p
	bad.Resource = "https://money.example/mcp/"
	requireOAuthCode(t, authCallError(t, h.c, ctx, "prepare_oauth_authorization", bad), "invalid_target")
	bad = p
	bad.CodeChallengeMethod = "plain"
	authCallError(t, h.c, ctx, "prepare_oauth_authorization", bad)
	code := authCall[core.OAuthAuthorizationCodeResult](t, h.c, ctx, "create_oauth_authorization_code", p)
	exchange := core.ExchangeOAuthCodeParams{ClientID: p.ClientID, Code: code.Code, RedirectURI: p.RedirectURI, CodeVerifier: strings.Repeat("x", 64)}
	requireOAuthCode(t, authCallError(t, h.c, context.Background(), "exchange_oauth_code", exchange), "invalid_grant")
	exchange.CodeVerifier = verifier
	requireOAuthCode(t, authCallError(t, h.c, context.Background(), "exchange_oauth_code", exchange), "invalid_grant")
	tokens := h.issueOAuth(t, ctx, "bags:write family:write")
	principal := authCall[core.Principal](t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: tokens.AccessToken})
	mcp := core.WithPrincipal(context.Background(), principal)
	authCall[core.BagResult](t, h.c, mcp, "create_bag", core.CreateBagParams{RequestID: "mcp-bag", Name: "Food"})
	authCall[core.CreateInvitationResult](t, h.c, mcp, "create_invitation", core.CreateInvitationParams{RequestID: "mcp-invite"})
	authCall[map[string]bool](t, h.c, ctx, "delete_user", core.DeleteUserParams{UserID: session.User.ID, RequestID: "delete"})
	authCallError(t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: tokens.AccessToken})
	authCallError(t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: p.ClientID, RefreshToken: tokens.RefreshToken})
}

func TestAuthCleanupPreservesRefreshReuseDetectionAcrossExpiry(t *testing.T) {
	h := newAuthHarness(t)
	_, ctx := h.register(t, "alice")
	first := h.issueOAuth(t, ctx, "bags:read")
	*h.clock = h.clock.Add(29 * 24 * time.Hour)
	second := authCall[core.OAuthTokens](t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: first.RefreshToken})
	*h.clock = h.clock.Add(2 * 24 * time.Hour)
	third := authCall[core.OAuthTokens](t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: second.RefreshToken})
	admin := core.WithPrincipal(context.Background(), core.Principal{Admin: true, Source: "cli"})
	authCall[map[string]any](t, h.c, admin, "cleanup_auth", map[string]any{})
	requireOAuthCode(t, authCallError(t, h.c, context.Background(), "refresh_oauth_token", core.RefreshOAuthTokenParams{ClientID: h.resolver.client.ClientID, RefreshToken: first.RefreshToken}), "invalid_grant")
	authCallError(t, h.c, context.Background(), "authenticate_oauth_token", core.TokenParams{Token: third.AccessToken})
}
