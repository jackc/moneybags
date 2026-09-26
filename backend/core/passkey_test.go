package core_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/jackc/moneybags/backend/core"
)

type softwareAuthenticator struct {
	key     *ecdsa.PrivateKey
	id      []byte
	counter uint32
}

func newAuthenticator(t *testing.T) *softwareAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softwareAuthenticator{key: key, id: id}
}
func encodeURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func passkeyChallenge(t *testing.T, options core.PasskeyOptions) string {
	t.Helper()
	raw, err := json.Marshal(options.Options)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.PublicKey.Challenge
}
func (a *softwareAuthenticator) register(t *testing.T, challenge, origin string) json.RawMessage {
	t.Helper()
	client, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": challenge, "origin": origin, "crossOrigin": false})
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: a.key.X.FillBytes(make([]byte, 32)), -3: a.key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte("money.example"))
	authData := append([]byte(nil), rpHash[:]...)
	authData = append(authData, 0x45, 0, 0, 0, 0)
	authData = append(authData, make([]byte, 16)...)
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(a.id)))
	authData = append(authData, a.id...)
	authData = append(authData, cose...)
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": encodeURL(a.id), "rawId": encodeURL(a.id), "type": "public-key", "authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{"credProps": map[string]bool{"rk": true}}, "response": map[string]any{"clientDataJSON": encodeURL(client), "attestationObject": encodeURL(attestation), "transports": []string{"internal"}}})
	return raw
}
func (a *softwareAuthenticator) login(t *testing.T, challenge, origin, userID string) json.RawMessage {
	t.Helper()
	a.counter++
	client, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte("money.example"))
	authData := append([]byte(nil), rpHash[:]...)
	authData = append(authData, 0x05)
	authData = binary.BigEndian.AppendUint32(authData, a.counter)
	clientHash := sha256.Sum256(client)
	signed := append(append([]byte(nil), authData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": encodeURL(a.id), "rawId": encodeURL(a.id), "type": "public-key", "authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": encodeURL(client), "authenticatorData": encodeURL(authData), "signature": encodeURL(signature), "userHandle": encodeURL([]byte(userID))}})
	return raw
}

func TestPasskeyRegistrationLoginMultipleKeysAndRevocation(t *testing.T) {
	h := newAuthHarness(t)
	session, ctx := h.register(t, "alice")
	first := newAuthenticator(t)
	second := newAuthenticator(t)
	var saved []core.PasskeyInfo
	for i, authenticator := range []*softwareAuthenticator{first, second} {
		options := authCall[core.PasskeyOptions](t, h.c, ctx, "begin_passkey_registration", map[string]any{})
		credential := authenticator.register(t, passkeyChallenge(t, options), "https://money.example")
		key := authCall[core.PasskeyInfo](t, h.c, ctx, "finish_passkey_registration", core.FinishPasskeyRegistrationParams{ChallengeID: options.ChallengeID, Credential: credential, Name: []string{"Phone", "Laptop"}[i]})
		saved = append(saved, key)
		authCallError(t, h.c, ctx, "finish_passkey_registration", core.FinishPasskeyRegistrationParams{ChallengeID: options.ChallengeID, Credential: credential})
	}
	listed := authCall[struct {
		Items []core.PasskeyInfo `json:"items"`
	}](t, h.c, ctx, "list_passkeys", map[string]any{})
	if len(listed.Items) != 2 {
		t.Fatalf("expected 2 passkeys, got %d", len(listed.Items))
	}
	options := authCall[core.PasskeyOptions](t, h.c, context.Background(), "begin_passkey_login", map[string]any{})
	credential := first.login(t, passkeyChallenge(t, options), "https://money.example", session.User.ID)
	authenticated := authCall[core.AuthSession](t, h.c, context.Background(), "finish_passkey_login", core.FinishPasskeyLoginParams{ChallengeID: options.ChallengeID, Credential: credential})
	if authenticated.User.ID != session.User.ID || authenticated.Token == "" {
		t.Fatal("passkey did not authenticate its owner")
	}
	authCallError(t, h.c, context.Background(), "finish_passkey_login", core.FinishPasskeyLoginParams{ChallengeID: options.ChallengeID, Credential: credential})
	authCall[map[string]bool](t, h.c, ctx, "delete_passkey", core.DeletePasskeyParams{PasskeyID: saved[0].ID})
	options = authCall[core.PasskeyOptions](t, h.c, context.Background(), "begin_passkey_login", map[string]any{})
	credential = first.login(t, passkeyChallenge(t, options), "https://money.example", session.User.ID)
	authCallError(t, h.c, context.Background(), "finish_passkey_login", core.FinishPasskeyLoginParams{ChallengeID: options.ChallengeID, Credential: credential})
	options = authCall[core.PasskeyOptions](t, h.c, context.Background(), "begin_passkey_login", map[string]any{})
	credential = second.login(t, passkeyChallenge(t, options), "https://money.example", session.User.ID)
	authCall[core.AuthSession](t, h.c, context.Background(), "finish_passkey_login", core.FinishPasskeyLoginParams{ChallengeID: options.ChallengeID, Credential: credential})
}
func TestPasskeyChallengeOriginExpiryAndOwnerBinding(t *testing.T) {
	h := newAuthHarness(t)
	alice, actx := h.register(t, "alice")
	_, bctx := h.register(t, "bob")
	authenticator := newAuthenticator(t)
	options := authCall[core.PasskeyOptions](t, h.c, actx, "begin_passkey_registration", map[string]any{})
	credential := authenticator.register(t, passkeyChallenge(t, options), "https://money.example")
	requireAuthCode(t, authCallError(t, h.c, bctx, "finish_passkey_registration", core.FinishPasskeyRegistrationParams{ChallengeID: options.ChallengeID, Credential: credential}), "invalid_passkey_challenge")
	options = authCall[core.PasskeyOptions](t, h.c, actx, "begin_passkey_registration", map[string]any{})
	credential = authenticator.register(t, passkeyChallenge(t, options), "https://evil.example")
	requireAuthCode(t, authCallError(t, h.c, actx, "finish_passkey_registration", core.FinishPasskeyRegistrationParams{ChallengeID: options.ChallengeID, Credential: credential}), "passkey_verification_failed")
	options = authCall[core.PasskeyOptions](t, h.c, context.Background(), "begin_passkey_login", map[string]any{})
	*h.clock = h.clock.Add(6 * time.Minute)
	credential = authenticator.login(t, passkeyChallenge(t, options), "https://money.example", alice.User.ID)
	requireAuthCode(t, authCallError(t, h.c, context.Background(), "finish_passkey_login", core.FinishPasskeyLoginParams{ChallengeID: options.ChallengeID, Credential: credential}), "invalid_passkey_challenge")
}
