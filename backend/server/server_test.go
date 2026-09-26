package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/fsblob"
	"github.com/jackc/moneybags/backend/memstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(ctx context.Context, id string) (core.OAuthClient, error) {
	return core.OAuthClient{ClientID: id, ClientName: "Test assistant", RedirectURIs: []string{"https://client.example/callback"}}, nil
}

type fixture struct {
	t      *testing.T
	app    *core.Core
	server *httptest.Server
	cookie *http.Cookie
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	blobs, e := fsblob.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	app := core.New(core.Config{Store: memstore.New(), Blobs: blobs, Origin: "http://localhost", RPID: "localhost", RPName: "Money Bags", OAuthResolver: fakeResolver{}})
	srv := httptest.NewServer(New(app, Config{Origin: "http://localhost", AssetsDir: t.TempDir()}))
	t.Cleanup(srv.Close)
	f := &fixture{t: t, app: app, server: srv}
	f.call("register", map[string]any{"username": "alice", "password": "a very long password", "family_name": "Family", "time_zone": "America/Chicago"}, 200)
	return f
}
func (f *fixture) call(name string, params any, status int) map[string]any {
	f.t.Helper()
	body, _ := json.Marshal(params)
	req, _ := http.NewRequest("POST", f.server.URL+"/api/actions/"+name, bytes.NewReader(body))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	if f.cookie != nil {
		req.AddCookie(f.cookie)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		f.t.Fatal(e)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		f.t.Fatalf("%s: status %d, want %d: %s", name, res.StatusCode, status, raw)
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "moneybags_session" {
			f.cookie = cookie
		}
	}
	out := map[string]any{}
	if e = json.Unmarshal(raw, &out); e != nil {
		f.t.Fatal(e)
	}
	return out
}

func TestHTTPCookiesCSRFAndCatalog(t *testing.T) {
	f := newFixture(t)
	if !f.cookie.HttpOnly || f.cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("insecure session cookie")
	}
	me := f.call("whoami", map[string]any{}, 200)
	if _, ok := me["token"]; ok {
		t.Fatal("session secret in response")
	}
	f.call("authenticate_session", map[string]any{"token": f.cookie.Value}, 404)
	f.call("cleanup_attachments", map[string]any{}, 404)
	req, _ := http.NewRequest("POST", f.server.URL+"/api/actions/create_bag", strings.NewReader(`{"request_id":"x","name":"bad"}`))
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.cookie)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-origin status %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", f.server.URL+"/mcp", strings.NewReader(`{}`))
	req.AddCookie(f.cookie)
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "oauth-protected-resource") {
		t.Fatal("MCP must demand bearer authentication")
	}
}

func TestUploadDownloadAndEntryRetry(t *testing.T) {
	f := newFixture(t)
	bag := f.call("create_bag", map[string]any{"request_id": "bag", "name": "Groceries", "initial_amount_cents": 100000}, 200)
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("request_id", "upload")
	file, _ := writer.CreateFormFile("file", "receipt.html")
	_, _ = file.Write([]byte("<script>alert('untrusted')</script>"))
	writer.Close()
	req, _ := http.NewRequest("POST", f.server.URL+"/api/uploads", &payload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "http://localhost")
	req.AddCookie(f.cookie)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	var upload map[string]any
	json.NewDecoder(res.Body).Decode(&upload)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("upload: %d %+v", res.StatusCode, upload)
	}
	params := map[string]any{"request_id": "shopping", "bag_id": bag["id"], "amount_cents": -4250, "notes": "| Item | Amount |\n|---|---|\n| Coffee | $900 |", "attachment_upload_ids": []any{upload["upload_id"]}}
	entry := f.call("create_entry", params, 200)
	retry := f.call("create_entry", params, 200)
	if retry["id"] != entry["id"] || retry["replayed"] != true || entry["balance_cents"] != float64(95750) {
		t.Fatalf("entry/retry mismatch: %+v %+v", entry, retry)
	}
	attachment := entry["attachments"].([]any)[0].(map[string]any)
	if _, ok := attachment["data"]; ok {
		t.Fatal("bytes leaked into entry")
	}
	if _, ok := attachment["blob_key"]; ok {
		t.Fatal("storage key exposed")
	}
	req, _ = http.NewRequest("GET", f.server.URL+"/api/attachments/"+attachment["id"].(string), nil)
	req.AddCookie(f.cookie)
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(data) != "<script>alert('untrusted')</script>" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;") || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe download %q %+v", data, res.Header)
	}
	f.call("delete_entry", map[string]any{"request_id": "delete", "entry_id": entry["id"], "expected_version": 1}, 200)
	retry = f.call("create_entry", params, 200)
	if retry["deleted"] != true {
		t.Fatal("deleted entry was recreated")
	}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(copy)
}
func (f *fixture) grant(scope string) string {
	f.t.Helper()
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	params := core.OAuthAuthorizationParams{ResponseType: "code", ClientID: "https://client.example/metadata", RedirectURI: "https://client.example/callback", Scope: scope, State: "state-12345678", CodeChallenge: base64.RawURLEncoding.EncodeToString(hash[:]), CodeChallengeMethod: "S256", Resource: "http://localhost/mcp"}
	f.call("prepare_oauth_authorization", params, 200)
	code := f.call("create_oauth_authorization_code", params, 200)
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {params.ClientID}, "redirect_uri": {params.RedirectURI}, "code": {code["code"].(string)}, "code_verifier": {verifier}, "resource": {params.Resource}}
	res, e := http.PostForm(f.server.URL+"/oauth/token", values)
	if e != nil {
		f.t.Fatal(e)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		f.t.Fatalf("token %d %s", res.StatusCode, raw)
	}
	var token core.OAuthTokens
	if e = json.Unmarshal(raw, &token); e != nil {
		f.t.Fatal(e)
	}
	return token.AccessToken
}
func TestMCPHTTPParityOAuthAndScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bag := f.call("create_bag", map[string]any{"request_id": "bag", "name": "Groceries", "initial_amount_cents": 100000}, 200)
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.grant("bags:read bags:write")}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	registered := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		registered[tool.Name] = tool
	}
	for _, info := range f.app.Actions() {
		if info.MCP && registered[info.Name] == nil {
			t.Errorf("missing tool %s", info.Name)
		}
		if !info.MCP && registered[info.Name] != nil {
			t.Errorf("exposed private action %s", info.Name)
		}
	}
	if !registered["list_bags"].Annotations.ReadOnlyHint || registered["create_entry"].Annotations.ReadOnlyHint {
		t.Fatal("incorrect annotations")
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_entry", Arguments: map[string]any{"request_id": "mcp-spend", "bag_id": bag["id"], "amount_cents": -4250}})
	if e != nil || result.IsError {
		t.Fatalf("MCP write: %v %+v", e, result)
	}
	entry := f.call("list_entries", map[string]any{"bag_id": bag["id"]}, 200)
	if len(entry["entries"].([]any)) != 2 {
		t.Fatal("MCP and HTTP do not share history")
	}
	bag = f.call("get_bag", map[string]any{"bag_id": bag["id"]}, 200)
	if bag["balance_cents"] != float64(95750) {
		t.Fatalf("balance %+v", bag)
	}
	readClient := mcp.NewClient(&mcp.Implementation{Name: "read-only-test", Version: "1"}, nil)
	read, e := readClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.grant("bags:read")}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer read.Close()
	result, e = read.CallTool(ctx, &mcp.CallToolParams{Name: "create_entry", Arguments: map[string]any{"request_id": "denied", "bag_id": bag["id"], "amount_cents": -1}})
	if e != nil || !result.IsError {
		t.Fatalf("read-only token wrote: %v %+v", e, result)
	}
}
