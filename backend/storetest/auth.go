package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"testing"
	"time"
)

func callAs[T any](t *testing.T, c *core.Core, ctx context.Context, name string, params any) T {
	t.Helper()
	var out T
	raw, e := json.Marshal(params)
	if e != nil {
		t.Fatal(e)
	}
	data, e := c.InvokeJSON(ctx, name, raw)
	if e != nil {
		t.Fatalf("%s: %v", name, e)
	}
	if e = json.Unmarshal(data, &out); e != nil {
		t.Fatal(e)
	}
	return out
}

// RunAuth exercises explicit global/family commit boundaries through both real adapters.
func RunAuth(t *testing.T, store core.Store) {
	t.Helper()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	c := core.New(core.Config{Store: store, Clock: func() time.Time { return now }, Origin: "http://localhost:8080", RPID: "localhost"})
	prefix := fmt.Sprintf("auth%d", time.Now().UnixNano())
	password := "correct horse battery staple"
	a := callAs[core.AuthSession](t, c, context.Background(), "register", core.RegisterParams{Username: prefix + "a", Password: password, FamilyName: "Shared family", TimeZone: "America/Chicago"})
	p := callAs[core.Principal](t, c, context.Background(), "authenticate_session", core.TokenParams{Token: a.Token})
	actx := core.WithPrincipal(context.Background(), p)
	invite := callAs[core.CreateInvitationResult](t, c, actx, "create_invitation", core.CreateInvitationParams{RequestID: "join"})
	b := callAs[core.AuthSession](t, c, context.Background(), "accept_invitation", core.AcceptInvitationParams{Token: invite.Token, Username: prefix + "b", Password: password})
	if b.Family.ID != a.Family.ID {
		t.Fatal("invited account changed families")
	}
	login := callAs[core.AuthSession](t, c, context.Background(), "login", core.LoginParams{Username: prefix + "b", Password: password})
	bp := callAs[core.Principal](t, c, context.Background(), "authenticate_session", core.TokenParams{Token: login.Token})
	bctx := core.WithPrincipal(context.Background(), bp)
	bag := callAs[core.BagResult](t, c, actx, "create_bag", core.CreateBagParams{RequestID: "auth-bag", Name: "Shared"})
	amount := int64(-100)
	entry := callAs[core.EntryResult](t, c, actx, "create_entry", core.CreateEntryParams{RequestID: "auth-entry", BagID: bag.ID, AmountCents: &amount, Notes: "Authorship must survive deletion"})
	callAs[map[string]bool](t, c, bctx, "delete_user", core.DeleteUserParams{RequestID: "delete-first-user", UserID: a.User.ID})
	survives := callAs[core.EntryResult](t, c, bctx, "get_entry", core.GetEntryParams{EntryID: entry.ID})
	if survives.AuthorID != a.User.ID || survives.Notes != entry.Notes || survives.AmountCents != -100 {
		t.Fatal("account deletion changed historical family data")
	}
	for _, name := range []string{"authenticate_session", "whoami"} {
		ctx := actx
		params := any(core.EmptyParams{})
		if name == "authenticate_session" {
			ctx = context.Background()
			params = core.TokenParams{Token: a.Token}
		}
		raw, _ := json.Marshal(params)
		if _, e := c.InvokeJSON(ctx, name, raw); e == nil {
			t.Fatalf("deleted user retained %s access", name)
		}
	}
	name := "Renamed by invited member"
	callAs[core.AuthFamily](t, c, bctx, "update_family", core.UpdateFamilyParams{RequestID: "rename", ExpectedVersion: 1, Name: &name})
}
