package core

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jackc/moneybags/backend/domain"
)

func (c *Core) deleteBag(ctx context.Context, p DeleteBagParams) (any, error) {
	return c.mutate(ctx, "delete_bag", p.RequestID, p, nil, func(tx Tx, _ Principal) (any, string, []string, error) {
		bag, err := loadBag(tx, p.BagID, false)
		if err != nil {
			return nil, "", nil, err
		}
		if err = checkedVersion(p.ExpectedVersion, bag.Version, bag); err != nil {
			return nil, "", nil, err
		}
		entries, err := listRecords[domain.Entry](tx, "entries")
		if err != nil {
			return nil, "", nil, err
		}
		ids := map[string]bool{}
		for _, entry := range entries {
			if entry.BagID == bag.ID {
				ids[entry.ID] = true
			}
		}
		removed, err := deleteEntries(tx, ids)
		if err != nil {
			return nil, "", nil, err
		}
		preferences, err := listRecords[BagPreferences](tx, "bag_preferences")
		if err != nil {
			return nil, "", nil, err
		}
		for _, prefs := range preferences {
			if slices.Contains(prefs.PinnedBagIDs, bag.ID) {
				prefs.PinnedBagIDs = slices.DeleteFunc(prefs.PinnedBagIDs, func(id string) bool { return id == bag.ID })
				if err = tx.Put("bag_preferences", prefs.UserID, prefs); err != nil {
					return nil, "", nil, err
				}
			}
		}
		keys, err := listRecords[idempotencyRecord](tx, "idempotency")
		if err != nil {
			return nil, "", nil, err
		}
		for _, key := range keys {
			if key.TargetID == bag.ID {
				key.Result, _ = json.Marshal(map[string]any{"applied": true, "deleted": true, "bag_id": bag.ID})
				if err = tx.Put("idempotency", key.ID, key); err != nil {
					return nil, "", nil, err
				}
			}
		}
		if err = tx.Delete("bags", bag.ID); err != nil {
			return nil, "", nil, err
		}
		return map[string]any{"deleted": true, "bag_id": bag.ID}, bag.ID, removed, nil
	})
}

// Delete dependent records together and return blob keys for removal after commit.
// Keep request keys as tombstones so retries cannot restore deleted content.
func deleteEntries(tx Tx, ids map[string]bool) ([]string, error) {
	files, err := listRecords[domain.Attachment](tx, "attachments")
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, file := range files {
		if ids[file.EntryID] {
			if err = tx.Delete("attachments", file.ID); err != nil {
				return nil, err
			}
			removed = append(removed, file.BlobKey)
		}
	}
	revisions, err := listRecords[domain.Revision](tx, "revisions")
	if err != nil {
		return nil, err
	}
	for _, revision := range revisions {
		if ids[revision.EntryID] {
			if err = tx.Delete("revisions", revision.ID); err != nil {
				return nil, err
			}
		}
	}
	uploads, err := listRecords[domain.Upload](tx, "uploads")
	if err != nil {
		return nil, err
	}
	consumed := map[string]bool{}
	for _, upload := range uploads {
		if ids[upload.ConsumedEntryID] {
			consumed[upload.ID] = true
			if err = tx.Delete("uploads", upload.ID); err != nil {
				return nil, err
			}
		}
	}
	keys, err := listRecords[idempotencyRecord](tx, "idempotency")
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if ids[key.TargetID] || consumed[key.TargetID] {
			field := "entry_id"
			if consumed[key.TargetID] {
				field = "upload_id"
			}
			key.Result, _ = json.Marshal(map[string]any{"applied": true, "deleted": true, field: key.TargetID})
			if err = tx.Put("idempotency", key.ID, key); err != nil {
				return nil, err
			}
		}
	}
	for id := range ids {
		if err = tx.Delete("entries", id); err != nil {
			return nil, err
		}
	}
	return removed, nil
}
