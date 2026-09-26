package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/jackc/moneybags/backend/memstore"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failStore struct {
	core.Store
	kind string
}
type failTx struct {
	core.Tx
	kind string
}

func (s failStore) InTx(ctx context.Context, family string, fn func(core.Tx) error) error {
	return s.Store.InTx(ctx, family, func(tx core.Tx) error { return fn(failTx{Tx: tx, kind: s.kind}) })
}
func (t failTx) Put(kind, id string, v any) error {
	if kind == t.kind {
		return errors.New("injected " + kind + " persistence failure")
	}
	return t.Tx.Put(kind, id, v)
}
func financeFixture(t *testing.T) (*memstore.Store, context.Context, core.Config) {
	t.Helper()
	store := memstore.New()
	ctx := core.WithPrincipal(context.Background(), core.Principal{UserID: "user", FamilyID: "family", Source: "web"})
	if e := store.InTx(ctx, "family", func(tx core.Tx) error {
		if e := tx.Put("users", "user", domain.User{ID: "user", FamilyID: "family", Username: "user"}); e != nil {
			return e
		}
		if e := tx.Put("families", "family", domain.Family{ID: "family", TimeZone: "America/Chicago"}); e != nil {
			return e
		}
		return tx.Put("bags", "bag", domain.Bag{ID: "bag", FamilyID: "family", NormalizedName: "bag", Version: 1})
	}); e != nil {
		t.Fatal(e)
	}
	var seq atomic.Int64
	cfg := core.Config{Store: store, Clock: func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }, ID: func() string { return fmt.Sprintf("id%d", seq.Add(1)) }, Origin: "http://localhost:8080", RPID: "localhost"}
	return store, ctx, cfg
}
func invokeFinance(t *testing.T, c *core.Core, ctx context.Context, action string, params any) map[string]any {
	t.Helper()
	raw, e := json.Marshal(params)
	if e != nil {
		t.Fatal(e)
	}
	result, e := c.InvokeJSON(ctx, action, raw)
	if e != nil {
		t.Fatalf("%s: %v", action, e)
	}
	var out map[string]any
	if e = json.Unmarshal(result, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestAuditAndIdempotencyFailureRollBackFinancialWrite(t *testing.T) {
	for _, kind := range []string{"audit", "idempotency", "revisions"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, cfg := financeFixture(t)
			cfg.Store = failStore{Store: store, kind: kind}
			c := core.New(cfg)
			_, e := c.InvokeJSON(ctx, "create_entry", json.RawMessage(`{"request_id":"fail","bag_id":"bag","amount_cents":-42}`))
			if e == nil {
				t.Fatal("injected persistence failure succeeded")
			}
			if e = store.InTx(ctx, "family", func(tx core.Tx) error {
				for _, record := range []string{"entries", "revisions", "audit", "idempotency"} {
					rows, e := tx.List(record)
					if e != nil {
						return e
					}
					if len(rows) != 0 {
						return fmt.Errorf("failed transaction left %s rows", record)
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestAmountsAndDefaultsReplayAfterMidnight(t *testing.T) {
	store, ctx, cfg := financeFixture(t)
	now := time.Date(2026, 9, 27, 4, 59, 0, 0, time.UTC)
	cfg.Clock = func() time.Time { return now }
	c := core.New(cfg)
	params := map[string]any{"request_id": "midnight", "bag_id": "bag", "amount_cents": 0}
	a := invokeFinance(t, c, ctx, "create_entry", params)
	now = now.Add(2 * time.Minute)
	b := invokeFinance(t, c, ctx, "create_entry", params)
	if a["date"] != "2026-09-26" || b["date"] != a["date"] || b["replayed"] != true || b["calculated_at"] != a["calculated_at"] {
		t.Fatal(a, b)
	}
	if e := store.InTx(ctx, "family", func(tx core.Tx) error { return tx.Delete("users", "user") }); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(params)
	if _, e := c.InvokeJSON(ctx, "create_entry", raw); e == nil {
		t.Fatal("deleted user replay retained access")
	}
}
func TestBalanceOverflowAndNegativeSpending(t *testing.T) {
	_, ctx, cfg := financeFixture(t)
	c := core.New(cfg)
	a := invokeFinance(t, c, ctx, "create_entry", map[string]any{"request_id": "positive", "bag_id": "bag", "amount_cents": domain.MaxSafeCents})
	if a["amount_cents"] != float64(domain.MaxSafeCents) {
		t.Fatal(a)
	}
	_, e := c.InvokeJSON(ctx, "create_entry", json.RawMessage(`{"request_id":"overflow","bag_id":"bag","amount_cents":1}`))
	var app *core.Error
	if !errors.As(e, &app) || app.Code != "validation_error" {
		t.Fatal(e)
	}
	invokeFinance(t, c, ctx, "delete_entry", map[string]any{"request_id": "remove", "entry_id": a["id"], "expected_version": 1})
	negative := invokeFinance(t, c, ctx, "create_entry", map[string]any{"request_id": "negative", "bag_id": "bag", "amount_cents": -4300})
	if negative["balance_cents"] != float64(-4300) {
		t.Fatal(negative)
	}
}
func TestUnknownFieldsAndMCPReadOnlyScope(t *testing.T) {
	_, ctx, cfg := financeFixture(t)
	c := core.New(cfg)
	if _, e := c.InvokeJSON(ctx, "create_entry", json.RawMessage(`{"request_id":"bad","bag_id":"bag","amount_cents":1,"currency":"USD"}`)); e == nil {
		t.Fatal("unexpected currency accepted")
	}
	ctx = core.WithPrincipal(ctx, core.Principal{UserID: "user", FamilyID: "family", Source: "mcp", Scopes: []string{"bags:read"}})
	invokeFinance(t, c, ctx, "get_bag", map[string]any{"bag_id": "bag"})
	if _, e := c.InvokeJSON(ctx, "create_entry", json.RawMessage(`{"request_id":"bad","bag_id":"bag","amount_cents":1}`)); e == nil {
		t.Fatal("read-only grant wrote an entry")
	}
}

type financeBlobs struct{ data map[string][]byte }

func (b *financeBlobs) PutBlob(_ context.Context, key string, data []byte) error {
	b.data[key] = data
	return nil
}
func (b *financeBlobs) GetBlob(_ context.Context, key string) ([]byte, error) {
	v, ok := b.data[key]
	if !ok {
		return nil, core.ErrNotFound
	}
	return v, nil
}
func (b *financeBlobs) DeleteBlob(_ context.Context, key string) error {
	delete(b.data, key)
	return nil
}

type financeFetcher struct {
	data  []byte
	calls int
	fail  bool
}

func (f *financeFetcher) Fetch(_ context.Context, _ string) ([]byte, string, error) {
	f.calls++
	if f.fail {
		return nil, "", errors.New("expired URL")
	}
	return f.data, "text/plain", nil
}
func TestMinimalFileDescriptorAndURLRefreshReplay(t *testing.T) {
	_, ctx, cfg := financeFixture(t)
	fetcher := &financeFetcher{data: []byte("receipt")}
	cfg.Fetcher = fetcher
	cfg.Blobs = &financeBlobs{data: map[string][]byte{}}
	c := core.New(cfg)
	request := strings.Repeat("r", 200)
	p := map[string]any{"request_id": request, "bag_id": "bag", "amount_cents": -1, "files": []core.FileSource{{FileID: "file1", DownloadURL: "https://files.example/first-secret"}}}
	created := invokeFinance(t, c, ctx, "create_entry", p)
	attachments := created["attachments"].([]any)
	if len(attachments) != 1 || attachments[0].(map[string]any)["file_name"] != "attachment" {
		t.Fatal(created)
	}
	fetcher.fail = true
	p["files"] = []core.FileSource{{FileID: "file1", DownloadURL: "https://files.example/refreshed-secret"}}
	replayed := invokeFinance(t, c, ctx, "create_entry", p)
	if replayed["replayed"] != true || fetcher.calls != 1 {
		t.Fatal("retry fetched temporary URL again")
	}
	fetcher.fail = false
	invokeFinance(t, c, ctx, "update_entry", map[string]any{"request_id": request, "entry_id": created["id"], "expected_version": 1, "files": []core.FileSource{{FileID: "file2", DownloadURL: "https://files.example/another-secret"}}})
	if fetcher.calls != 2 {
		t.Fatal("different parent action collided with staged-file request key")
	}
}
func TestRemoteFilesRequestLimitIsAtomic(t *testing.T) {
	store, ctx, cfg := financeFixture(t)
	cfg.Fetcher = &financeFetcher{data: make([]byte, domain.MaxFileBytes)}
	cfg.Blobs = &financeBlobs{data: map[string][]byte{}}
	c := core.New(cfg)
	files := []core.FileSource{}
	for i := 0; i < 5; i++ {
		files = append(files, core.FileSource{FileID: fmt.Sprintf("file%d", i), DownloadURL: "https://files.example/content"})
	}
	raw, _ := json.Marshal(map[string]any{"request_id": "oversize-remote", "bag_id": "bag", "amount_cents": -1, "files": files})
	_, e := c.InvokeJSON(ctx, "create_entry", raw)
	var app *core.Error
	if !errors.As(e, &app) || app.Code != "validation_error" {
		t.Fatal(e)
	}
	if e = store.InTx(ctx, "family", func(tx core.Tx) error {
		rows, e := tx.List("entries")
		if e != nil {
			return e
		}
		if len(rows) != 0 {
			return errors.New("oversized file request partially recorded an entry")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestUploadExpiryActorBindingAndDeletedStageReplay(t *testing.T) {
	store, ctx, cfg := financeFixture(t)
	now := cfg.Clock()
	cfg.Clock = func() time.Time { return now }
	cfg.Blobs = &financeBlobs{data: map[string][]byte{}}
	c := core.New(cfg)
	upload := invokeFinance(t, c, ctx, "stage_attachment", core.StageAttachmentParams{RequestID: "stage", FileName: "receipt.txt", Data: []byte("receipt")})
	if e := store.InTx(ctx, "family", func(tx core.Tx) error { return tx.Put("users", "other", domain.User{ID: "other", FamilyID: "family"}) }); e != nil {
		t.Fatal(e)
	}
	p := map[string]any{"request_id": "link", "bag_id": "bag", "amount_cents": 0, "attachment_upload_ids": []string{upload["upload_id"].(string)}}
	otherCtx := core.WithPrincipal(ctx, core.Principal{UserID: "other", FamilyID: "family", Source: "web"})
	raw, _ := json.Marshal(p)
	if _, e := c.InvokeJSON(otherCtx, "create_entry", raw); e == nil {
		t.Fatal("another actor consumed a staged upload")
	}
	now = now.Add(25 * time.Hour)
	if _, e := c.InvokeJSON(ctx, "create_entry", raw); e == nil {
		t.Fatal("expired upload consumed")
	}
	fresh := invokeFinance(t, c, ctx, "stage_attachment", core.StageAttachmentParams{RequestID: "fresh", FileName: "receipt.txt", Data: []byte("receipt")})
	p["attachment_upload_ids"] = []string{fresh["upload_id"].(string)}
	created := invokeFinance(t, c, ctx, "create_entry", p)
	invokeFinance(t, c, ctx, "delete_entry", map[string]any{"request_id": "delete", "entry_id": created["id"], "expected_version": 1})
	replayed := invokeFinance(t, c, ctx, "stage_attachment", core.StageAttachmentParams{RequestID: "fresh", FileName: "receipt.txt", Data: []byte("receipt")})
	if replayed["deleted"] != true || replayed["attachment"] != nil {
		t.Fatal("deleted entry retained attachment metadata in stage retry")
	}
	if e := store.InTx(ctx, "family", func(tx core.Tx) error {
		var upload domain.Upload
		return tx.Get("uploads", fresh["upload_id"].(string), &upload)
	}); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("deleted entry retained consumed upload metadata")
	}
}
func TestArchiveAndCreationSerialize(t *testing.T) {
	for i := 0; i < 20; i++ {
		_, ctx, cfg := financeFixture(t)
		c := core.New(cfg)
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() {
			<-start
			_, e := c.InvokeJSON(ctx, "create_entry", json.RawMessage(`{"request_id":"create","bag_id":"bag","amount_cents":1}`))
			done <- e
		}()
		go func() {
			<-start
			_, e := c.InvokeJSON(ctx, "archive_bag", json.RawMessage(`{"request_id":"archive","bag_id":"bag","expected_version":1}`))
			done <- e
		}()
		close(start)
		rejected := false
		for j := 0; j < 2; j++ {
			if e := <-done; e != nil {
				var app *core.Error
				if !errors.As(e, &app) || app.Code != "bag_archived" {
					t.Fatal(e)
				}
				rejected = true
			}
		}
		bag := invokeFinance(t, c, ctx, "get_bag", map[string]any{"bag_id": "bag"})
		want := float64(1)
		if rejected {
			want = 0
		}
		if bag["archived"] != true || bag["balance_cents"] != want {
			t.Fatal(bag)
		}
	}
}
