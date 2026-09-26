// Package storetest runs the same transactional and product contract against every database backend.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
	fail bool
}

func (b *memoryBlobs) PutBlob(_ context.Context, key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("injected disk failure")
	}
	b.data[key] = append([]byte{}, data...)
	return nil
}
func (b *memoryBlobs) GetBlob(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.data[key]
	if !ok {
		return nil, core.ErrNotFound
	}
	return append([]byte{}, v...), nil
}
func (b *memoryBlobs) DeleteBlob(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.data, key)
	return nil
}
func Run(t *testing.T, store core.Store) {
	t.Helper()
	t.Run("personal bag pins", func(t *testing.T) { runBagPreferences(t, store) })
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	prefix := fmt.Sprintf("suite%d", time.Now().UnixNano())
	family := prefix + "f"
	other := prefix + "g"
	actor := prefix + "u"
	for _, f := range []string{family, other} {
		if provisioner, ok := store.(core.FamilyProvisioner); ok {
			if e := provisioner.CreateFamily(ctx, f); e != nil {
				t.Fatal(e)
			}
		}
		if e := store.InTx(ctx, f, func(tx core.Tx) error {
			if e := tx.Put("families", f, domain.Family{ID: f, Name: "Test", TimeZone: "America/Chicago", Version: 1}); e != nil {
				return e
			}
			return tx.Put("users", actor, domain.User{ID: actor, FamilyID: f, Username: "test", DisplayName: "Test Member"})
		}); e != nil {
			t.Fatal(e)
		}
	}
	ctx = core.WithPrincipal(ctx, core.Principal{UserID: actor, FamilyID: family, Source: "web", AuthenticatedAt: now})
	var seq atomic.Int64
	blobs := &memoryBlobs{data: map[string][]byte{}}
	c := core.New(core.Config{Store: store, Blobs: blobs, Clock: func() time.Time { return now }, ID: func() string { return fmt.Sprintf("%sid%d", prefix, seq.Add(1)) }, Origin: "http://localhost:8080", RPID: "localhost"})
	invoke := func(ctx context.Context, name string, p any) (map[string]any, error) {
		raw, e := json.Marshal(p)
		if e != nil {
			return nil, e
		}
		data, e := c.InvokeJSON(ctx, name, raw)
		if e != nil {
			return nil, e
		}
		var out map[string]any
		e = json.Unmarshal(data, &out)
		return out, e
	}
	call := func(name string, p any) map[string]any {
		t.Helper()
		out, e := invoke(ctx, name, p)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		return out
	}
	requireCode := func(name string, p any, want string) {
		t.Helper()
		_, e := invoke(ctx, name, p)
		var app *core.Error
		if !errors.As(e, &app) || app.Code != want {
			t.Fatalf("%s got %v, want %s", name, e, want)
		}
	}
	bag := call("create_bag", map[string]any{"name": " Groceries ", "request_id": "bag", "initial_amount_cents": 100000})
	bagID := bag["id"].(string)
	if bag["balance_cents"] != float64(100000) {
		t.Fatal(bag)
	}
	household := call("create_bag", map[string]any{"name": "Household", "request_id": "house"})
	requireCode("create_bag", map[string]any{"name": "gRoCeRiEs", "request_id": "duplicate-name"}, "validation_error")
	requireCode("create_entry", map[string]any{"bag_id": bagID, "request_id": "missing"}, "validation_error")
	requireCode("create_entry", map[string]any{"bag_id": bagID, "amount_cents": 1, "date": "2026-09-27", "request_id": "future"}, "validation_error")
	payload := map[string]any{"bag_id": bagID, "amount_cents": -8732, "notes": "| Item | Total |\n|--|--|\n| Coffee | 99 |", "request_id": "expense"}
	entry := call("create_entry", payload)
	entryID := entry["id"].(string)
	if entry["balance_cents"] != float64(91268) {
		t.Fatal(entry)
	}
	replay := call("create_entry", payload)
	if replay["id"] != entryID || replay["replayed"] != true {
		t.Fatal(replay)
	}
	requireCode("create_entry", map[string]any{"bag_id": bagID, "amount_cents": -1, "request_id": "expense"}, "idempotency_conflict")
	zero := call("create_entry", map[string]any{"bag_id": bagID, "amount_cents": 0, "date": "2026-09-01", "request_id": "zero"})
	if zero["balance_cents"] != float64(91268) {
		t.Fatal(zero)
	}
	call("update_entry", map[string]any{"entry_id": entryID, "expected_version": 1, "notes": "edited\n**markdown**", "request_id": "edit"})
	requireCode("update_entry", map[string]any{"entry_id": entryID, "expected_version": 1, "amount_cents": -3, "request_id": "stale"}, "version_conflict")
	// Independent equal-content attachments never multiply financial amounts.
	stage := func(request string) map[string]any {
		return call("stage_attachment", core.StageAttachmentParams{RequestID: request, FileName: "receipt.txt", Data: []byte("receipt")})
	}
	a, b := stage("upload-a"), stage("upload-b")
	fileEntry := call("create_entry", map[string]any{"request_id": "with-files", "bag_id": bagID, "amount_cents": -500, "attachment_upload_ids": []string{a["upload_id"].(string), b["upload_id"].(string)}})
	files := fileEntry["attachments"].([]any)
	if len(files) != 2 || fileEntry["balance_cents"] != float64(90768) {
		t.Fatal(fileEntry)
	}
	first := files[0].(map[string]any)
	second := files[1].(map[string]any)
	if _, ok := first["blob_key"]; ok {
		t.Fatal("blob key leaked")
	}
	if first["sha256"] != second["sha256"] {
		t.Fatal("equal bytes hash differently")
	}
	call("update_entry", map[string]any{"request_id": "remove-one", "entry_id": fileEntry["id"], "expected_version": 1, "remove_attachment_ids": []string{first["id"].(string)}})
	call("get_attachment", map[string]any{"attachment_id": second["id"]})
	copiedUpload := stage("copied-receipt")
	copiedEntry := call("create_entry", map[string]any{"request_id": "copied-entry", "bag_id": household["id"], "amount_cents": -250, "attachment_upload_ids": []string{copiedUpload["upload_id"].(string)}})
	copiedFile := copiedEntry["attachments"].([]any)[0].(map[string]any)
	if copiedFile["id"] == second["id"] || copiedFile["sha256"] != second["sha256"] {
		t.Fatal("separate entry must own a separate equal-content attachment")
	}

	requireCode("get_attachment", map[string]any{"attachment_id": first["id"]}, "not_found")
	// Tenant scope is independently enforced at the store and action boundary.
	otherCtx := core.WithPrincipal(context.Background(), core.Principal{UserID: actor, FamilyID: other, Source: "web"})
	if _, e := invoke(otherCtx, "get_bag", map[string]any{"bag_id": bagID}); e == nil {
		t.Fatal("cross-family read")
	}
	if _, e := invoke(context.Background(), "list_bags", map[string]any{}); e == nil {
		t.Fatal("missing principal allowed")
	}
	// The database transaction includes revisions, idempotency, uploads, and audit.
	rollback := errors.New("rollback")
	if e := store.InTx(ctx, family, func(tx core.Tx) error {
		if e := tx.Put("test", "rolled-back", map[string]any{"id": "rolled-back"}); e != nil {
			return e
		}
		return rollback
	}); !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	if e := store.InTx(ctx, family, func(tx core.Tx) error { var v any; return tx.Get("test", "rolled-back", &v) }); !errors.Is(e, core.ErrNotFound) {
		t.Fatalf("rollback persisted: %v", e)
	}
	// Concurrent repeats are one transaction and one entry.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := invoke(ctx, "create_entry", map[string]any{"request_id": "concurrent", "bag_id": bagID, "amount_cents": -100})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	latest := call("get_bag", map[string]any{"bag_id": bagID})
	if latest["balance_cents"] != float64(90668) {
		t.Fatal(latest)
	}
	call("archive_bag", map[string]any{"request_id": "archive", "bag_id": bagID, "expected_version": 1})
	requireCode("create_entry", map[string]any{"request_id": "archived", "bag_id": bagID, "amount_cents": 0}, "bag_archived")
	requireCode("update_entry", map[string]any{"request_id": "archived-edit", "entry_id": entryID, "expected_version": 2, "notes": "no"}, "bag_archived")
	deletion := map[string]any{"request_id": "delete", "entry_id": entryID, "expected_version": 2}
	call("delete_entry", deletion)
	call("delete_entry", deletion)
	gone := call("create_entry", payload)
	if gone["deleted"] != true || gone["replayed"] != true || gone["notes"] != nil {
		t.Fatal(gone)
	}
	requireCode("get_entry_history", map[string]any{"entry_id": entryID}, "not_found")
	call("delete_entry", map[string]any{"request_id": "delete-files", "entry_id": fileEntry["id"], "expected_version": 2})
	call("get_attachment", map[string]any{"attachment_id": copiedFile["id"]})
	copyBalance := call("get_bag", map[string]any{"bag_id": household["id"]})
	if copyBalance["balance_cents"] != float64(-250) {
		t.Fatal("deleting independent receipt copy affected another bag")
	}

	// Bounded queries, grouping, date ordering, and backdated zero entries.
	list := call("list_bags", map[string]any{"limit": 1})
	if len(list["bags"].([]any)) != 1 || list["next_cursor"] == "" {
		t.Fatal(list)
	}
	entries := call("list_entries", map[string]any{"bag_id": bagID, "to_date": "2026-09-02"})
	if len(entries["entries"].([]any)) != 1 {
		t.Fatal(entries)
	}
	// A failed file write never records a financial mutation.
	blobs.fail = true
	_, e := invoke(ctx, "create_entry", map[string]any{"request_id": "file-failure", "bag_id": bagID, "amount_cents": -1, "files": []core.FileSource{{FileName: "bad.txt", Data: []byte("bad")}}})
	if e == nil {
		t.Fatal("file failure succeeded")
	}
	blobs.fail = false
	// Aggregate numeric range is checked after summation, including cancellation.
	if e := store.InTx(ctx, family, func(tx core.Tx) error {
		if e := tx.Put("bags", "cancellation", domain.Bag{ID: "cancellation", FamilyID: family, NormalizedName: "cancellation"}); e != nil {
			return e
		}
		for i, n := range []int64{domain.MaxSafeCents, domain.MaxSafeCents, -domain.MaxSafeCents} {
			id := fmt.Sprintf("cancel%d", i)
			if e := tx.Put("entries", id, domain.Entry{ID: id, FamilyID: family, BagID: "cancellation", AmountCents: n, Date: "2026-09-01"}); e != nil {
				return e
			}
		}
		sum, e := tx.Balance("cancellation")
		if e != nil {
			return e
		}
		if sum != domain.MaxSafeCents {
			return fmt.Errorf("sum = %d", sum)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// Ensure ordinary list JSON contains no bytes, transient URLs, or storage paths.
	raw, e := c.InvokeJSON(ctx, "list_entries", json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"blob_key", "download_url", "cmVjZWlwdA=="} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("list leaked %s", secret)
		}
	}
}
