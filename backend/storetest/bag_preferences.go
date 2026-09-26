package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
)

func runBagPreferences(t *testing.T, store core.Store) {
	t.Helper()
	family := fmt.Sprintf("pins%d", time.Now().UnixNano())
	for _, id := range []string{family, family + "other"} {
		if provisioner, ok := store.(core.FamilyProvisioner); ok {
			if err := provisioner.CreateFamily(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.InTx(context.Background(), id, func(tx core.Tx) error {
			for _, user := range []string{"one", "two"} {
				if err := tx.Put("users", user, domain.User{ID: user, FamilyID: id}); err != nil {
					return err
				}
			}
			if id != family {
				return nil
			}
			for _, bag := range []string{"groceries", "car"} {
				if err := tx.Put("bags", bag, domain.Bag{ID: bag, FamilyID: id, Name: bag, NormalizedName: bag, Version: 1}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	actor := core.Principal{UserID: "one", FamilyID: family, Source: "web"}
	otherUser := actor
	otherUser.UserID = "two"
	otherFamily := actor
	otherFamily.FamilyID += "other"
	readOnly := actor
	readOnly.Source, readOnly.Scopes = "mcp", []string{"bags:read"}
	call := func(actor core.Principal, action string, params any, code string) core.BagPreferences {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		// A fresh core instance on every call verifies persisted preferences.
		app := core.New(core.Config{Store: store})
		result, err := app.InvokeJSON(core.WithPrincipal(context.Background(), actor), action, raw)
		if code != "" {
			var problem *core.Error
			if !errors.As(err, &problem) || problem.Code != code {
				t.Fatalf("%s: got %v, want %s", action, err, code)
			}
			return core.BagPreferences{}
		}
		if err != nil {
			t.Fatal(err)
		}
		var prefs core.BagPreferences
		if err := json.Unmarshal(result, &prefs); err != nil {
			t.Fatal(err)
		}
		return prefs
	}
	read := func(actor core.Principal, want []string) {
		t.Helper()
		prefs := call(actor, "get_bag_preferences", map[string]any{}, "")
		if prefs.UserID != actor.UserID || prefs.FamilyID != actor.FamilyID || !reflect.DeepEqual(prefs.PinnedBagIDs, want) {
			t.Fatalf("preferences = %+v, want %v for %+v", prefs, want, actor)
		}
	}
	pin := func(actor core.Principal, bag string, pinned bool, request string, code string) {
		t.Helper()
		call(actor, "set_bag_pin", map[string]any{"request_id": request, "bag_id": bag, "pinned": pinned}, code)
	}
	read(actor, []string{})
	pin(actor, "groceries", true, "first", "")
	pin(actor, "groceries", true, "first", "") // An unchanged retry cannot duplicate pins.
	pin(actor, "car", true, "second", "")
	read(actor, []string{"groceries", "car"})
	read(otherUser, []string{})
	pin(otherUser, "car", true, "first", "")
	read(actor, []string{"groceries", "car"})
	read(otherUser, []string{"car"})
	read(otherFamily, []string{})
	pin(otherFamily, "groceries", true, "foreign", "not_found")
	pin(readOnly, "car", false, "readonly", "permission_denied")
	call(actor, "set_bag_pin", map[string]any{"request_id": "missing", "bag_id": "car"}, "validation_error")
	call(actor, "set_bag_pin", map[string]any{"request_id": "spoof", "bag_id": "car", "pinned": true, "user_id": "two"}, "validation_error")
	pin(actor, "missing", true, "missing-bag", "not_found")
	pin(actor, "groceries", false, "remove", "")
	pin(actor, "groceries", true, "first", "") // Replay must not undo a later unpin.
	read(actor, []string{"car"})
	pin(actor, "groceries", true, "repin", "")
	read(actor, []string{"car", "groceries"})
	call(actor, "archive_bag", map[string]any{"request_id": "archive", "bag_id": "car", "expected_version": 1}, "")
	pin(actor, "car", true, "archived", "bag_archived")
	pin(actor, "car", false, "remove-archived", "")
	pin(actor, "groceries", false, "remove-last", "")
	read(actor, []string{})
	if err := store.InTx(context.Background(), family, func(tx core.Tx) error { return tx.Delete("users", actor.UserID) }); err != nil {
		t.Fatal(err)
	}
	call(actor, "get_bag_preferences", map[string]any{}, "permission_denied")
	pin(actor, "groceries", true, "first", "permission_denied")
}
