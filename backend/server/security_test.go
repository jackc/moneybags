package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/memstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStaticBootstrapCSPHashesExactReleaseBytes(t *testing.T) {
	dir := t.TempDir()
	script := "\n\tconst start = import('/_app/start.js');\n"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><script>"+script+"</script></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := New(core.New(core.Config{Store: memstore.New()}), Config{Origin: "https://money.example", SecureCookies: true, AssetsDir: dir})
	request := httptest.NewRequest(http.MethodGet, "https://money.example/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	sum := sha256.Sum256([]byte(script))
	policy := response.Header().Get("Content-Security-Policy")
	expected := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(policy, expected) {
		t.Fatalf("missing exact bootstrap hash: %s", policy)
	}
	scripts := strings.Split(strings.Split(policy, "script-src ")[1], ";")[0]
	if strings.Contains(scripts, "unsafe-inline") {
		t.Fatal("CSP permits arbitrary inline scripts")
	}
	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("secure deployment lacks HSTS")
	}
}

func TestAllCookieActionsRequireSameOriginIncludingLogin(t *testing.T) {
	handler := New(core.New(core.Config{Store: memstore.New()}), Config{Origin: "https://money.example"})
	for _, origin := range []string{"", "https://attacker.example", "null"} {
		for _, name := range []string{"register", "login", "whoami", "create_oauth_authorization_code", "logout"} {
			request := httptest.NewRequest(http.MethodPost, "https://money.example/api/actions/"+name, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 403 {
				t.Errorf("%s origin %q status %d", name, origin, response.Code)
			}
		}
	}
}

type errorStore struct{}

func (errorStore) InTx(context.Context, string, func(core.Tx) error) error {
	return errors.New("password=database-secret token=secret-file-url")
}
func TestInfrastructureErrorsAndLogsNeverRevealSecrets(t *testing.T) {
	var logs bytes.Buffer
	handler := New(core.New(core.Config{AllowRegistration: true, Store: errorStore{}}), Config{Origin: "https://money.example", Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	request := httptest.NewRequest(http.MethodPost, "https://money.example/api/actions/register", strings.NewReader(`{"username":"alice","password":"correct password here"}`))
	request.Header.Set("Origin", "https://money.example")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 500 {
		t.Fatalf("got status %d", response.Code)
	}
	for _, secret := range []string{"database-secret", "secret-file-url", "correct password here"} {
		if strings.Contains(response.Body.String()+logs.String(), secret) {
			t.Errorf("secret exposed: %s", secret)
		}
	}
	if !strings.Contains(logs.String(), "request_id") {
		t.Fatal("safe request ID absent from logs")
	}
}

func TestMCPFileMetadataAndCredentialMutationHints(t *testing.T) {
	f := newFixture(t)
	client := mcp.NewClient(&mcp.Implementation{Name: "file-contract-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.grant("bags:read bags:write")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "create_entry" || tool.Name == "update_entry" {
			raw, _ := json.Marshal(tool)
			var parsed struct {
				Meta struct {
					Files []string `json:"openai/fileParams"`
				} `json:"_meta"`
				Schema struct {
					Properties map[string]struct {
						Items struct {
							Required   []string       `json:"required"`
							Properties map[string]any `json:"properties"`
						} `json:"items"`
					} `json:"properties"`
				} `json:"inputSchema"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(parsed.Meta.Files, []string{"files"}) {
				t.Fatalf("missing ChatGPT file metadata for %s", tool.Name)
			}
			file := parsed.Schema.Properties["files"].Items
			if !slices.Equal(file.Required, []string{"download_url", "file_id"}) {
				t.Fatalf("wrong file required fields: %v", file.Required)
			}
			for _, name := range []string{"download_url", "file_id", "mime_type", "file_name"} {
				if file.Properties[name] == nil {
					t.Errorf("%s missing %s property", tool.Name, name)
				}
			}
		}
		if strings.Contains(tool.Name, "passkey") {
			t.Errorf("credential primitive %s exposed as MCP tool", tool.Name)
		}
	}
}

func TestMCPActionLogsAreCorrelatedAndRedacted(t *testing.T) {
	f := newFixture(t)
	f.server.Close()
	var logs bytes.Buffer
	f.server = httptest.NewServer(New(f.app, Config{Origin: "http://localhost", AssetsDir: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(&logs, nil))}))
	t.Cleanup(f.server.Close)
	token := f.grant("bags:read bags:write")
	client := mcp.NewClient(&mcp.Implementation{Name: "logging-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_entry", Arguments: map[string]any{"request_id": "request-secret-value", "bag_id": "nonexistent", "amount_cents": -1, "notes": "private receipt notes", "files": []any{map[string]any{"download_url": "https://files.example/receipt?secret=file-secret-value", "file_id": "file-secret-id", "file_name": "private-file-name"}}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(logs.String(), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["source"] == "mcp" && record["action"] == "create_entry" {
			found = true
			if record["request_id"] == "" || record["duration_ms"] == nil || record["status"] != "error" {
				t.Fatalf("incomplete MCP action log %+v", record)
			}
		}
	}
	if !found {
		t.Fatalf("MCP action not logged: %s", logs.String())
	}
	for _, secret := range []string{token, f.cookie.Value, "request-secret-value", "private receipt notes", "file-secret-value", "file-secret-id", "private-file-name"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("MCP log exposed input/credential %q", secret)
		}
	}
}
