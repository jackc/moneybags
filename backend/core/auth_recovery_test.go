package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/memstore"
)

type authFaultStore struct {
	base               *memstore.Store
	failCommit         int
	calls              int
	rejectGlobalWrites bool
	missingFamily      string
	reportFamilies     bool
}

func (s *authFaultStore) InTx(ctx context.Context, family string, fn func(core.Tx) error) error {
	s.calls++
	call := s.calls
	return s.base.InTx(ctx, family, func(tx core.Tx) error {
		wrapped := &authFaultTx{Tx: tx}
		if err := fn(wrapped); err != nil {
			return err
		}
		if call == s.failCommit || family == "" && wrapped.written && s.rejectGlobalWrites {
			return errors.New("injected commit failure")
		}
		return nil
	})
}
func (s *authFaultStore) FamilyIDs(ctx context.Context) ([]string, error) {
	if !s.reportFamilies {
		return nil, nil
	}
	return []string{s.missingFamily}, nil
}

type authFaultTx struct {
	core.Tx
	written bool
}

func (t *authFaultTx) Put(kind, id string, v any) error {
	t.written = true
	return t.Tx.Put(kind, id, v)
}
func (t *authFaultTx) Delete(kind, id string) error { t.written = true; return t.Tx.Delete(kind, id) }
func TestRegistrationCrossFileCommitFailuresFailClosed(t *testing.T) {
	for _, boundary := range []int{1, 2, 3} {
		t.Run(string(rune('0'+boundary)), func(t *testing.T) {
			store := &authFaultStore{base: memstore.New(), failCommit: boundary}
			c := core.New(core.Config{Store: store, Origin: "https://money.example"})
			params := core.RegisterParams{Username: "alice", Password: testPassword}
			authCallError(t, c, context.Background(), "register", params)
			store.failCommit = 0
			authCallError(t, c, context.Background(), "login", core.LoginParams{Username: "alice", Password: testPassword})
			if boundary < 3 {
				authCall[core.AuthSession](t, c, context.Background(), "register", params)
			} else {
				var dirs []map[string]any
				if err := store.base.InTx(context.Background(), "", func(tx core.Tx) error {
					rows, err := tx.List("directory")
					if err != nil {
						return err
					}
					for _, raw := range rows {
						var dir map[string]any
						if err := json.Unmarshal(raw, &dir); err != nil {
							return err
						}
						dirs = append(dirs, dir)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if len(dirs) != 1 || dirs[0]["active"] != false {
					t.Fatal("activation fault failed to retain inactive reservation")
				}
				family := dirs[0]["family_id"].(string)
				store.reportFamilies = true
				store.missingFamily = family
				admin := core.WithPrincipal(context.Background(), core.Principal{Admin: true, Source: "cli"})
				result := authCall[struct {
					Pending []string `json:"pending_user_ids"`
				}](t, c, admin, "reconcile_auth", map[string]any{})
				if len(result.Pending) != 1 {
					t.Fatal("pending activation not reported for operator recovery")
				}
				authCallError(t, c, context.Background(), "login", core.LoginParams{Username: "alice", Password: testPassword})
			}
		})
	}
}
func TestAccountDeletionSurvivesGlobalRevocationFailure(t *testing.T) {
	base := memstore.New()
	fault := &authFaultStore{base: base}
	c := core.New(core.Config{Store: fault, Origin: "https://money.example"})
	session := authCall[core.AuthSession](t, c, context.Background(), "register", core.RegisterParams{Username: "alice", Password: testPassword})
	principal := authCall[core.Principal](t, c, context.Background(), "authenticate_session", core.TokenParams{Token: session.Token})
	ctx := core.WithPrincipal(context.Background(), principal)
	fault.rejectGlobalWrites = true
	authCallError(t, c, ctx, "delete_user", core.DeleteUserParams{UserID: session.User.ID, RequestID: "delete"})
	fault.rejectGlobalWrites = false
	authCallError(t, c, context.Background(), "authenticate_session", core.TokenParams{Token: session.Token})
	authCallError(t, c, context.Background(), "login", core.LoginParams{Username: "alice", Password: testPassword})
	fault.reportFamilies = true
	fault.missingFamily = session.Family.ID
	admin := core.WithPrincipal(context.Background(), core.Principal{Admin: true, Source: "cli"})
	result := authCall[struct {
		Removed []string `json:"removed_user_ids"`
	}](t, c, admin, "reconcile_auth", map[string]any{})
	if len(result.Removed) != 1 || result.Removed[0] != session.User.ID {
		t.Fatal("stale global routing was not repaired")
	}
}
func TestReconciliationDoesNotCreateMissingFamilyDatabase(t *testing.T) {
	base := memstore.New()
	fault := &authFaultStore{base: base}
	c := core.New(core.Config{Store: fault, Origin: "https://money.example"})
	session := authCall[core.AuthSession](t, c, context.Background(), "register", core.RegisterParams{Username: "alice", Password: testPassword})
	before := fault.calls
	admin := core.WithPrincipal(context.Background(), core.Principal{Admin: true, Source: "cli"})
	result := authCall[struct {
		Unreachable []string `json:"unreachable_family_ids"`
	}](t, c, admin, "reconcile_auth", map[string]any{})
	if len(result.Unreachable) != 1 || result.Unreachable[0] != session.Family.ID {
		t.Fatal("missing database not reported")
	}
	if fault.calls-before != 1 {
		t.Fatal("reconciliation opened a missing family database")
	}
}
