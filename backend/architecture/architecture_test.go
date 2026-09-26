// Package architecture checks the boundaries that every transport must share.
package architecture

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/memstore"
)

func backendDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source path unavailable")
	}
	return filepath.Dir(filepath.Dir(file))
}
func TestDomainCoreAndTransportDependencyBoundaries(t *testing.T) {
	root := backendDirectory(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		layer := strings.Split(filepath.ToSlash(relative), "/")[0]
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			local := strings.TrimPrefix(name, "github.com/jackc/moneybags/backend/")
			switch layer {
			case "domain":
				if strings.Contains(name, ".") || strings.HasPrefix(name, "os") || strings.HasPrefix(name, "net") || strings.HasPrefix(name, "io") || name == "database/sql" {
					t.Errorf("pure domain imports I/O/dependency %s in %s", name, relative)
				}
			case "core":
				for _, denied := range []string{"server", "pgstore", "jedstore", "dualstore", "memstore", "fsblob", "safefetch"} {
					if local == denied {
						t.Errorf("core imports adapter %s in %s", name, relative)
					}
				}
				if name == "net/http" || name == "database/sql" || name == "os" || strings.HasPrefix(name, "github.com/jackc/pgx") || strings.HasPrefix(name, "github.com/jackc/jed") {
					t.Errorf("core implements I/O through %s in %s", name, relative)
				}
			case "server":
				for _, denied := range []string{"pgstore", "jedstore", "dualstore", "memstore", "fsblob", "safefetch"} {
					if local == denied {
						t.Errorf("transport bypasses core via %s in %s", name, relative)
					}
				}
				if name == "database/sql" || strings.HasPrefix(name, "github.com/jackc/pgx") || strings.HasPrefix(name, "github.com/jackc/jed") {
					t.Errorf("transport executes database I/O via %s", name)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && pkg.Name == "core" && selector.Sel.Name == "New" && layer != "storetest" {
				t.Errorf("%s creates another core; adapters must share the composition root's instance", relative)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProductCatalogParityAndPrivateDefault(t *testing.T) {
	app := core.New(core.Config{Store: memstore.New()})
	catalog := map[string]core.ActionInfo{}
	for _, action := range app.Actions() {
		if catalog[action.Name].Name != "" {
			t.Fatalf("duplicate action %s", action.Name)
		}
		catalog[action.Name] = action
		if action.NewParams == nil {
			t.Errorf("action %s lacks typed parameters", action.Name)
		}
		if action.MaxPayloadBytes < 1 {
			t.Errorf("action %s lacks a payload bound", action.Name)
		}
		if action.Administrative && (action.MCP || action.Web || action.Public) {
			t.Errorf("administrative action %s exposed", action.Name)
		}
		if !action.Public {
			_, err := app.InvokeJSON(context.Background(), action.Name, []byte(`{}`))
			var problem *core.Error
			if !errors.As(err, &problem) || problem.Code != "permission_denied" {
				t.Errorf("private action %s did not fail closed before dispatch: %v", action.Name, err)
			}
		}
	}
	for _, name := range []string{"whoami", "get_family", "list_bags", "get_bag", "create_bag", "update_bag", "archive_bag", "unarchive_bag", "list_entries", "get_entry", "get_entry_history", "create_entry", "update_entry", "delete_entry", "stage_attachment", "list_attachments", "get_attachment", "list_users", "delete_user", "update_family", "create_invitation", "list_invitations", "revoke_invitation", "list_connections", "revoke_connection"} {
		action, ok := catalog[name]
		if !ok || !action.Web || !action.MCP || action.Public {
			t.Errorf("ordinary product action %s does not share authenticated web and MCP exposure", name)
		}
		if action.Permission == "" {
			t.Errorf("product action %s lacks scope", name)
		}
	}
	for _, name := range []string{"register", "login", "logout", "accept_invitation", "authenticate_session", "begin_passkey_registration", "finish_passkey_registration", "begin_passkey_login", "finish_passkey_login", "list_passkeys", "update_passkey", "delete_passkey", "change_password", "reauthenticate", "prepare_oauth_authorization", "create_oauth_authorization_code", "exchange_oauth_code", "refresh_oauth_token", "revoke_oauth_token", "authenticate_oauth_token"} {
		action, ok := catalog[name]
		if !ok || action.MCP {
			t.Errorf("authentication primitive %s is absent or exposed as an assistant tool", name)
		}
	}
	for _, name := range []string{"authenticate_session", "authenticate_oauth_token", "exchange_oauth_code", "refresh_oauth_token", "revoke_oauth_token"} {
		if catalog[name].Web {
			t.Errorf("protocol primitive %s exposed by generic browser action route", name)
		}
	}
}
