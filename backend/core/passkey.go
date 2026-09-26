package core

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type passkeyRecord struct {
	ID         string              `json:"id"`
	FamilyID   string              `json:"family_id"`
	UserID     string              `json:"user_id"`
	Name       string              `json:"name"`
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  time.Time           `json:"created_at"`
}
type PasskeyInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
type passkeyChallenge struct {
	ID        string               `json:"id"`
	UserID    string               `json:"user_id,omitempty"`
	FamilyID  string               `json:"family_id,omitempty"`
	Kind      string               `json:"kind"`
	Session   webauthn.SessionData `json:"session"`
	ExpiresAt time.Time            `json:"expires_at"`
}
type PasskeyOptions struct {
	Options     any    `json:"options"`
	ChallengeID string `json:"challenge_id"`
}
type FinishPasskeyRegistrationParams struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
	Name        string          `json:"name"`
}
type FinishPasskeyLoginParams struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}
type DeletePasskeyParams struct {
	PasskeyID string `json:"passkey_id"`
}
type RenamePasskeyParams struct {
	PasskeyID string `json:"passkey_id"`
	Name      string `json:"name"`
}
type webAuthnUser struct {
	user        AuthUser
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte                         { return []byte(u.user.ID) }
func (u *webAuthnUser) WebAuthnName() string                       { return u.user.Username }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.user.DisplayName }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
func (c *Core) webAuthn() (*webauthn.WebAuthn, error) {
	if c.rpID == "" || c.origin == "" {
		return nil, authError("passkeys_unavailable", "Passkeys are not configured")
	}
	name := c.rpName
	if name == "" {
		name = "Money Bags"
	}
	return webauthn.New(&webauthn.Config{RPDisplayName: name, RPID: c.rpID, RPOrigins: []string{strings.TrimSuffix(c.origin, "/")}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
}
func (c *Core) loadWebAuthnUser(ctx context.Context, familyID, userID string) (*webAuthnUser, error) {
	var user AuthUser
	keys := []webauthn.Credential{}
	err := c.store.InTx(ctx, familyID, func(tx Tx) error {
		if err := tx.Get("users", userID, &user); err != nil {
			return err
		}
		all, err := listAuthRecords[passkeyRecord](tx, "passkeys")
		if err != nil {
			return err
		}
		for _, key := range all {
			if key.UserID == userID {
				keys = append(keys, key.Credential)
			}
		}
		return nil
	})
	return &webAuthnUser{user, keys}, err
}
func (c *Core) savePasskeyChallenge(ctx context.Context, user *AuthUser, kind string, session *webauthn.SessionData) (string, error) {
	session.Expires = c.now().Add(5 * time.Minute)
	record := passkeyChallenge{ID: c.token(), Kind: kind, Session: *session, ExpiresAt: session.Expires}
	if user != nil {
		record.UserID = user.ID
		record.FamilyID = user.FamilyID
	}
	err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Put("passkey_challenges", record.ID, record) })
	return record.ID, err
}
func (c *Core) consumePasskeyChallenge(ctx context.Context, id, kind, userID string) (passkeyChallenge, error) {
	var record passkeyChallenge
	err := c.store.InTx(ctx, "", func(tx Tx) error {
		if err := tx.Get("passkey_challenges", id, &record); err != nil {
			return err
		}
		return tx.Delete("passkey_challenges", id)
	})
	if err != nil || record.Kind != kind || record.UserID != userID || !record.ExpiresAt.After(c.now()) {
		return passkeyChallenge{}, authError("invalid_passkey_challenge", "Passkey challenge is invalid or expired")
	}
	return record, nil
}
func (c *Core) beginPasskeyRegistration(ctx context.Context, _ EmptyParams) (PasskeyOptions, error) {
	actor, err := c.requireRecent(ctx)
	if err != nil {
		return PasskeyOptions{}, err
	}
	wan, err := c.webAuthn()
	if err != nil {
		return PasskeyOptions{}, err
	}
	user, err := c.loadWebAuthnUser(ctx, actor.FamilyID, actor.UserID)
	if err != nil {
		return PasskeyOptions{}, err
	}
	options, session, err := wan.BeginRegistration(user, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired), webauthn.WithConveyancePreference(protocol.PreferNoAttestation), webauthn.WithExclusions(webauthn.Credentials(user.credentials).CredentialDescriptors()))
	if err != nil {
		return PasskeyOptions{}, err
	}
	id, err := c.savePasskeyChallenge(ctx, &user.user, "registration", session)
	return PasskeyOptions{options, id}, err
}
func (c *Core) finishPasskeyRegistration(ctx context.Context, p FinishPasskeyRegistrationParams) (PasskeyInfo, error) {
	actor, err := c.requireRecent(ctx)
	if err != nil {
		return PasskeyInfo{}, err
	}
	if utf8.RuneCountInString(strings.TrimSpace(p.Name)) > 100 {
		return PasskeyInfo{}, authInvalid("name must contain at most 100 characters")
	}
	wan, err := c.webAuthn()
	if err != nil {
		return PasskeyInfo{}, err
	}
	record, err := c.consumePasskeyChallenge(ctx, p.ChallengeID, "registration", actor.UserID)
	if err != nil {
		return PasskeyInfo{}, err
	}
	user, err := c.loadWebAuthnUser(ctx, actor.FamilyID, actor.UserID)
	if err != nil {
		return PasskeyInfo{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(p.Credential))
	if err != nil {
		return PasskeyInfo{}, authError("passkey_verification_failed", "Passkey verification failed")
	}
	credential, err := wan.CreateCredential(user, record.Session, parsed)
	if err != nil {
		return PasskeyInfo{}, authError("passkey_verification_failed", "Passkey verification failed")
	}
	key := passkeyRecord{ID: c.id(), FamilyID: actor.FamilyID, UserID: actor.UserID, Name: strings.TrimSpace(p.Name), Credential: *credential, CreatedAt: c.now()}
	if key.Name == "" {
		key.Name = "Passkey"
	}
	err = c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error {
		var active AuthUser
		if err := tx.Get("users", actor.UserID, &active); err != nil {
			return authDenied()
		}
		all, err := listAuthRecords[passkeyRecord](tx, "passkeys")
		if err != nil {
			return err
		}
		for _, existing := range all {
			if bytes.Equal(existing.Credential.ID, key.Credential.ID) {
				return authError("validation_error", "That passkey is already registered")
			}
		}
		return tx.Put("passkeys", key.ID, key)
	})
	return PasskeyInfo{key.ID, key.Name, key.CreatedAt}, err
}
func (c *Core) beginPasskeyLogin(ctx context.Context, _ EmptyParams) (PasskeyOptions, error) {
	wan, err := c.webAuthn()
	if err != nil {
		return PasskeyOptions{}, err
	}
	options, session, err := wan.BeginDiscoverableLogin()
	if err != nil {
		return PasskeyOptions{}, err
	}
	id, err := c.savePasskeyChallenge(ctx, nil, "login", session)
	return PasskeyOptions{options, id}, err
}
func (c *Core) finishPasskeyLogin(ctx context.Context, p FinishPasskeyLoginParams) (AuthSession, error) {
	wan, err := c.webAuthn()
	if err != nil {
		return AuthSession{}, err
	}
	record, err := c.consumePasskeyChallenge(ctx, p.ChallengeID, "login", "")
	if err != nil {
		return AuthSession{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(p.Credential))
	if err != nil {
		return AuthSession{}, authError("passkey_verification_failed", "Passkey verification failed")
	}
	var resolved *webAuthnUser
	resolver := func(_ []byte, handle []byte) (webauthn.User, error) {
		if len(handle) == 0 || len(handle) > 128 {
			return nil, authDenied()
		}
		var dir userDirectory
		if err := c.store.InTx(ctx, "", func(tx Tx) error { return tx.Get("directory", string(handle), &dir) }); err != nil || !dir.Active {
			return nil, authDenied()
		}
		var err error
		resolved, err = c.loadWebAuthnUser(ctx, dir.FamilyID, dir.ID)
		return resolved, err
	}
	_, credential, err := wan.ValidatePasskeyLogin(resolver, record.Session, parsed)
	if err != nil || resolved == nil || credential == nil || credential.Authenticator.CloneWarning {
		return AuthSession{}, authError("passkey_verification_failed", "Passkey verification failed")
	}
	err = c.store.InTx(ctx, resolved.user.FamilyID, func(tx Tx) error {
		var active AuthUser
		if err := tx.Get("users", resolved.user.ID, &active); err != nil {
			return authDenied()
		}
		keys, err := listAuthRecords[passkeyRecord](tx, "passkeys")
		if err != nil {
			return err
		}
		for _, key := range keys {
			if key.UserID == resolved.user.ID && bytes.Equal(key.Credential.ID, credential.ID) {
				key.Credential = *credential
				return tx.Put("passkeys", key.ID, key)
			}
		}
		return authDenied()
	})
	if err != nil {
		return AuthSession{}, err
	}
	return c.newAuthSession(ctx, resolved.user)
}
func (c *Core) registerPasskeyActions() {
	Register(c, "begin_passkey_registration", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, c.beginPasskeyRegistration)
	Register(c, "finish_passkey_registration", ActionInfo{Web: true, Mutation: true, Permission: "bags:read", MaxPayloadBytes: 128 << 10}, c.finishPasskeyRegistration)
	Register(c, "begin_passkey_login", ActionInfo{Web: true, Public: true, Mutation: true}, c.beginPasskeyLogin)
	Register(c, "finish_passkey_login", ActionInfo{Web: true, Public: true, Mutation: true, MaxPayloadBytes: 128 << 10}, c.finishPasskeyLogin)
	Register(c, "list_passkeys", ActionInfo{Web: true, Permission: "bags:read"}, func(ctx context.Context, p authListParams) (authPage[PasskeyInfo], error) {
		actor, _ := PrincipalFromContext(ctx)
		keys := []PasskeyInfo{}
		err := c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error {
			all, err := listAuthRecords[passkeyRecord](tx, "passkeys")
			if err != nil {
				return err
			}
			for _, key := range all {
				if key.UserID == actor.UserID {
					keys = append(keys, PasskeyInfo{key.ID, key.Name, key.CreatedAt})
				}
			}
			return nil
		})
		if err != nil {
			return authPage[PasskeyInfo]{}, err
		}
		return authPageOf(keys, p, func(k PasskeyInfo) string { return k.ID })
	})
	Register(c, "delete_passkey", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, func(ctx context.Context, p DeletePasskeyParams) (map[string]bool, error) {
		actor, err := c.requireRecent(ctx)
		if err != nil {
			return nil, err
		}
		err = c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error {
			var key passkeyRecord
			if err := tx.Get("passkeys", p.PasskeyID, &key); err != nil {
				return err
			}
			if key.UserID != actor.UserID {
				return ErrNotFound
			}
			return tx.Delete("passkeys", key.ID)
		})
		return map[string]bool{"deleted": true}, err
	})
	Register(c, "update_passkey", ActionInfo{Web: true, Mutation: true, Permission: "bags:read"}, func(ctx context.Context, p RenamePasskeyParams) (PasskeyInfo, error) {
		actor, _ := PrincipalFromContext(ctx)
		if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 100 {
			return PasskeyInfo{}, authInvalid("name must contain 1 to 100 characters")
		}
		var key passkeyRecord
		err := c.store.InTx(ctx, actor.FamilyID, func(tx Tx) error {
			if err := tx.Get("passkeys", p.PasskeyID, &key); err != nil {
				return err
			}
			if key.UserID != actor.UserID {
				return ErrNotFound
			}
			key.Name = strings.TrimSpace(p.Name)
			return tx.Put("passkeys", key.ID, key)
		})
		return PasskeyInfo{key.ID, key.Name, key.CreatedAt}, err
	})
}
