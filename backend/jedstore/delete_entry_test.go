package jedstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/jackc/moneybags/backend/jedstore"
)

func TestDeleteEntryAfterReopenWithOtherAttachments(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(fmt.Sprintf("attached=%v", attached), func(t *testing.T) {
			dir := t.TempDir()
			s, err := jedstore.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if s != nil {
					s.Close()
				}
			})
			ctx := core.WithPrincipal(context.Background(), core.Principal{FamilyID: "family", UserID: "user", Source: "web"})
			if err := s.CreateFamily(ctx, "family"); err != nil {
				t.Fatal(err)
			}
			err = s.InTx(ctx, "family", func(tx core.Tx) error {
				if err := tx.Put("users", "user", domain.User{ID: "user", FamilyID: "family"}); err != nil {
					return err
				}
				if err := tx.Put("bags", "bag", domain.Bag{ID: "bag", FamilyID: "family", NormalizedName: "bag", Version: 1}); err != nil {
					return err
				}
				for _, id := range []string{"delete-me", "keep-me"} {
					entry := domain.Entry{ID: id, FamilyID: "family", BagID: "bag", AmountCents: -100, Date: "2026-09-27", Version: 1}
					if err := tx.Put("entries", id, entry); err != nil {
						return err
					}
				}
				if attached {
					file := domain.Attachment{ID: "000-target", FamilyID: "family", EntryID: "delete-me", FileName: "receipt.txt"}
					if err := tx.Put("attachments", file.ID, file); err != nil {
						return err
					}
				}
				// Span multiple pages so deleting the target's attachment cannot
				// materialize every remaining child row as a side effect.
				for i := 0; i < 100; i++ {
					file := domain.Attachment{ID: fmt.Sprintf("other-%03d", i), FamilyID: "family", EntryID: "keep-me", FileName: strings.Repeat("receipt", 30)}
					if err := tx.Put("attachments", file.ID, file); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = jedstore.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			c := core.New(core.Config{Store: s})
			result, err := c.InvokeJSON(ctx, "delete_entry", json.RawMessage(`{"entry_id":"delete-me","expected_version":1,"request_id":"delete"}`))
			if err != nil {
				t.Fatal(err)
			}
			var deleted struct {
				Deleted      bool  `json:"deleted"`
				BalanceCents int64 `json:"balance_cents"`
			}
			if err := json.Unmarshal(result, &deleted); err != nil {
				t.Fatal(err)
			}
			if !deleted.Deleted || deleted.BalanceCents != -100 {
				t.Fatalf("unexpected deletion result: %s", result)
			}
			err = s.InTx(ctx, "family", func(tx core.Tx) error {
				var entry domain.Entry
				if err := tx.Get("entries", "delete-me", &entry); !errors.Is(err, core.ErrNotFound) {
					return fmt.Errorf("deleted entry remains: %v", err)
				}
				files, err := tx.List("attachments")
				if err != nil {
					return err
				}
				if len(files) != 100 {
					return fmt.Errorf("got %d attachments, want 100", len(files))
				}
				for _, raw := range files {
					var file domain.Attachment
					if err := json.Unmarshal(raw, &file); err != nil {
						return err
					}
					if file.EntryID != "keep-me" {
						return fmt.Errorf("unexpected remaining attachment: %s", file.ID)
					}
				}
				return tx.Get("entries", "keep-me", &entry)
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
