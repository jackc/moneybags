package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
)

func runDeleteBag(t *testing.T, store core.Store) {
	ctx := context.Background()
	family := fmt.Sprintf("deletebag%d", time.Now().UnixNano())
	for _, id := range []string{family, family + "other"} {
		if provisioner, ok := store.(core.FamilyProvisioner); ok {
			if err := provisioner.CreateFamily(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.InTx(ctx, id, func(tx core.Tx) error {
			if err := tx.Put("families", id, domain.Family{ID: id, TimeZone: "UTC"}); err != nil {
				return err
			}
			for _, user := range []string{"one", "two"} {
				if err := tx.Put("users", user, domain.User{ID: user, FamilyID: id}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	actor := core.Principal{FamilyID: family, UserID: "one", Source: "web"}
	ctx = core.WithPrincipal(ctx, actor)
	blobs := &memoryBlobs{data: map[string][]byte{}}
	app := core.New(core.Config{Store: store, Blobs: blobs})
	call := func(name string, params any) map[string]any {
		t.Helper()
		return callAs[map[string]any](t, app, ctx, name, params)
	}
	reject := func(p core.Principal, params any, code string) {
		t.Helper()
		raw, _ := json.Marshal(params)
		_, err := app.InvokeJSON(core.WithPrincipal(context.Background(), p), "delete_bag", raw)
		var problem *core.Error
		if !errors.As(err, &problem) || problem.Code != code {
			t.Fatalf("got %v, want %s", err, code)
		}
	}
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprintf("archived=%v", archived), func(t *testing.T) {
			// Distinct keys across the two runs, with identical names reusable after deletion.
			key := fmt.Sprintf("%v-", archived)
			create := map[string]any{"request_id": key + "bag", "name": "Delete me", "initial_amount_cents": 100}
			bag := call("create_bag", create)
			survivor := call("create_bag", map[string]any{"request_id": key + "keep", "name": key + "Keep me"})
			upload := core.StageAttachmentParams{RequestID: key + "upload", FileName: "receipt.txt", Data: []byte("private receipt")}
			staged := call("stage_attachment", upload)
			entryParams := map[string]any{"request_id": key + "entry", "bag_id": bag["id"], "amount_cents": -25, "notes": "private notes", "attachment_upload_ids": []string{staged["upload_id"].(string)}}
			entry := call("create_entry", entryParams)
			fileID := entry["attachments"].([]any)[0].(map[string]any)["id"].(string)
			call("update_entry", map[string]any{"request_id": key + "edit", "entry_id": entry["id"], "expected_version": 1, "notes": "changed notes"})
			kept := call("create_entry", map[string]any{"request_id": key + "keep-entry", "bag_id": survivor["id"], "amount_cents": 50, "files": []core.FileSource{{FileName: "copy.txt", Data: []byte("private receipt")}}})
			for _, user := range []string{"one", "two"} {
				p := actor
				p.UserID = user
				for _, id := range []any{bag["id"], survivor["id"]} {
					callAs[core.BagPreferences](t, app, core.WithPrincipal(context.Background(), p), "set_bag_pin", map[string]any{"request_id": key + fmt.Sprint(id), "bag_id": id, "pinned": true})
				}
			}
			version := 1
			if archived {
				call("archive_bag", map[string]any{"request_id": key + "archive", "bag_id": bag["id"], "expected_version": 1})
				version++
			}
			deletion := map[string]any{"request_id": key + "delete", "bag_id": bag["id"], "expected_version": version}
			other := actor
			other.FamilyID += "other"
			reject(other, deletion, "not_found")
			readonly := actor
			readonly.Source = "mcp"
			readonly.Scopes = []string{"bags:read"}
			reject(readonly, deletion, "permission_denied")
			reject(actor, map[string]any{"request_id": key + "stale", "bag_id": bag["id"], "expected_version": version + 1}, "version_conflict")
			reject(actor, map[string]any{"request_id": key + "missing-version", "bag_id": bag["id"]}, "validation_error")
			if got := call("get_bag", map[string]any{"bag_id": bag["id"]}); got["balance_cents"] != float64(75) {
				t.Fatal(got)
			}
			call("get_attachment", map[string]any{"attachment_id": fileID})
			writer := actor
			writer.Source = "mcp"
			writer.Scopes = []string{"bags:write"}
			writer.UserID = "two"
			deleted := callAs[map[string]any](t, app, core.WithPrincipal(context.Background(), writer), "delete_bag", deletion)
			replay := callAs[map[string]any](t, app, core.WithPrincipal(context.Background(), writer), "delete_bag", deletion)
			if deleted["deleted"] != true || replay["replayed"] != true {
				t.Fatal(deleted, replay)
			}
			for _, params := range []struct {
				name   string
				params any
			}{{"create_bag", create}, {"create_entry", entryParams}, {"stage_attachment", upload}} {
				got := call(params.name, params.params)
				if got["deleted"] != true || got["replayed"] != true || got["notes"] != nil || got["name"] != nil {
					t.Fatal(got)
				}
			}
			reject(actor, map[string]any{"request_id": key + "gone", "bag_id": bag["id"], "expected_version": version}, "not_found")
			for _, user := range []string{"one", "two"} {
				p := actor
				p.UserID = user
				prefs := callAs[core.BagPreferences](t, app, core.WithPrincipal(context.Background(), p), "get_bag_preferences", core.EmptyParams{})
				for _, id := range prefs.PinnedBagIDs {
					if id == bag["id"] {
						t.Fatal("deleted bag still pinned")
					}
				}
			}
			keptFile := kept["attachments"].([]any)[0].(map[string]any)["id"]
			call("get_attachment", map[string]any{"attachment_id": keptFile})
			if got := call("get_bag", map[string]any{"bag_id": survivor["id"]}); got["balance_cents"] != float64(50) {
				t.Fatal(got)
			}
			if err := store.InTx(ctx, family, func(tx core.Tx) error {
				var v any
				if err := tx.Get("bags", bag["id"].(string), &v); !errors.Is(err, core.ErrNotFound) {
					return fmt.Errorf("bag remains: %v", err)
				}
				if err := tx.Get("attachments", fileID, &v); !errors.Is(err, core.ErrNotFound) {
					return fmt.Errorf("attachment remains: %v", err)
				}
				for _, kind := range []string{"entries", "revisions", "uploads"} {
					rows, err := tx.List(kind)
					if err != nil {
						return err
					}
					for _, raw := range rows {
						var record map[string]any
						if err := json.Unmarshal(raw, &record); err != nil {
							return err
						}
						if record["bag_id"] == bag["id"] || record["entry_id"] == entry["id"] || record["consumed_entry_id"] == entry["id"] {
							return fmt.Errorf("%s remains: %s", kind, raw)
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			blobs.mu.Lock()
			count := len(blobs.data)
			blobs.mu.Unlock()
			want := 1
			if archived {
				want = 2
			}
			if count != want {
				t.Fatalf("blob count = %d, want %d", count, want)
			}
		})
	}
}
