package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/moneybags/backend/domain"
)

type PageParams struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type ListBagsParams struct {
	PageParams
	Archived *bool `json:"archived,omitempty"`
}
type GetBagParams struct {
	BagID string `json:"bag_id"`
}
type CreateBagParams struct {
	RequestID          string `json:"request_id"`
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	InitialAmountCents *int64 `json:"initial_amount_cents,omitempty"`
}
type UpdateBagParams struct {
	RequestID       string  `json:"request_id"`
	BagID           string  `json:"bag_id"`
	ExpectedVersion int64   `json:"expected_version"`
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
}
type ArchiveBagParams struct {
	RequestID       string `json:"request_id"`
	BagID           string `json:"bag_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type DeleteBagParams struct {
	RequestID       string `json:"request_id"`
	BagID           string `json:"bag_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type BagResult struct {
	domain.Bag
	BalanceCents int64     `json:"balance_cents"`
	CalculatedAt time.Time `json:"calculated_at"`
}
type EntryResult struct {
	domain.Entry
	Attachments  []domain.Attachment `json:"attachments"`
	BalanceCents int64               `json:"balance_cents"`
	CalculatedAt time.Time           `json:"calculated_at"`
}
type ListEntriesParams struct {
	PageParams
	BagID    string `json:"bag_id,omitempty"`
	FromDate string `json:"from_date,omitempty"`
	ToDate   string `json:"to_date,omitempty"`
}
type GetEntryParams struct {
	EntryID string `json:"entry_id"`
}
type EntryPageParams struct {
	PageParams
	EntryID string `json:"entry_id"`
}
type CreateEntryParams struct {
	RequestID           string       `json:"request_id"`
	BagID               string       `json:"bag_id"`
	AmountCents         *int64       `json:"amount_cents"`
	Date                string       `json:"date,omitempty"`
	Notes               string       `json:"notes,omitempty"`
	AttachmentUploadIDs []string     `json:"attachment_upload_ids,omitempty"`
	Files               []FileSource `json:"files,omitempty"`
}
type UpdateEntryParams struct {
	RequestID           string       `json:"request_id"`
	EntryID             string       `json:"entry_id"`
	ExpectedVersion     int64        `json:"expected_version"`
	AmountCents         *int64       `json:"amount_cents,omitempty"`
	Date                *string      `json:"date,omitempty"`
	Notes               *string      `json:"notes,omitempty"`
	AttachmentUploadIDs []string     `json:"attachment_upload_ids,omitempty"`
	RemoveAttachmentIDs []string     `json:"remove_attachment_ids,omitempty"`
	Files               []FileSource `json:"files,omitempty"`
}
type DeleteEntryParams struct {
	RequestID       string `json:"request_id"`
	EntryID         string `json:"entry_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type idempotencyRecord struct {
	ID        string          `json:"id"`
	FamilyID  string          `json:"family_id"`
	ActorID   string          `json:"actor_id"`
	Action    string          `json:"action"`
	RequestID string          `json:"request_id"`
	Hash      string          `json:"hash"`
	TargetID  string          `json:"target_id,omitempty"`
	Result    json.RawMessage `json:"result"`
	CreatedAt time.Time       `json:"created_at"`
}

type mutation func(Tx, Principal) (any, string, []string, error)

func (c *Core) mutate(ctx context.Context, action, requestID string, params any, prepare func() error, fn mutation) (any, error) {
	if strings.TrimSpace(requestID) == "" || len(requestID) > 200 {
		return nil, E("validation_error", "request_id is required and must be at most 200 characters")
	}
	p, ok := PrincipalFromContext(ctx)
	if !ok || p.FamilyID == "" || p.UserID == "" {
		return nil, E("permission_denied", "Authentication required")
	}
	raw, e := json.Marshal(params)
	if e != nil {
		return nil, e
	}
	var canonical any
	if e = json.Unmarshal(raw, &canonical); e != nil {
		return nil, e
	}
	canonical = stablePayload(canonical)
	raw, e = json.Marshal(canonical)
	if e != nil {
		return nil, e
	}
	hash := sha256.Sum256(raw)
	hashText := hex.EncodeToString(hash[:])
	keyHash := sha256.Sum256([]byte(p.UserID + "\x00" + action + "\x00" + requestID))
	key := hex.EncodeToString(keyHash[:])
	var result any
	var found bool
	lookup := func(tx Tx) (bool, error) {
		var user domain.User
		if e := tx.Get("users", p.UserID, &user); e != nil {
			return false, E("permission_denied", "Account no longer exists")
		}
		var old idempotencyRecord
		e := tx.Get("idempotency", key, &old)
		if errors.Is(e, ErrNotFound) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if old.Hash != hashText {
			return false, E("idempotency_conflict", "request_id was already used with different input")
		}
		var r map[string]any
		if e = json.Unmarshal(old.Result, &r); e != nil {
			return false, e
		}
		r["replayed"] = true
		result = r
		return true, nil
	}
	if e = c.store.InTx(ctx, p.FamilyID, func(tx Tx) error { var e error; found, e = lookup(tx); return e }); e != nil {
		return nil, e
	}
	if found {
		return result, nil
	}
	if prepare != nil {
		if e = prepare(); e != nil {
			return nil, e
		}
	}
	var deleteBlobs []string
	e = c.store.InTx(ctx, p.FamilyID, func(tx Tx) error {
		if found, e := lookup(tx); e != nil || found {
			return e
		}
		r, target, removed, e := fn(tx, p)
		if e != nil {
			return e
		}
		result = r
		encoded, e := json.Marshal(r)
		if e != nil {
			return e
		}
		now := c.now()
		audit := domain.Audit{ID: c.id(), FamilyID: p.FamilyID, ActorID: p.UserID, Action: action, AffectedIDs: []string{target}, Source: p.Source, RequestID: requestID, CreatedAt: now}
		if e = tx.Put("audit", audit.ID, audit); e != nil {
			return e
		}
		if e = tx.Put("idempotency", key, idempotencyRecord{ID: key, FamilyID: p.FamilyID, ActorID: p.UserID, Action: action, RequestID: requestID, Hash: hashText, TargetID: target, Result: encoded, CreatedAt: now}); e != nil {
			return e
		}
		deleteBlobs = removed
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, key := range deleteBlobs {
		if c.blobs != nil {
			_ = c.blobs.DeleteBlob(ctx, key)
		}
	}
	return result, nil
}

// Temporary credentials never contribute to a stable request identity or persisted result.
func stablePayload(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if k == "download_url" {
				continue
			}
			if k == "data" {
				raw, _ := json.Marshal(x)
				sum := sha256.Sum256(raw)
				out[k+"_sha256"] = hex.EncodeToString(sum[:])
				continue
			}
			out[k] = stablePayload(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = stablePayload(x)
		}
		return out
	default:
		return v
	}
}
func (c *Core) read(ctx context.Context, fn func(Tx, Principal) (any, error)) (any, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok || p.UserID == "" || p.FamilyID == "" {
		return nil, E("permission_denied", "Authentication required")
	}
	var result any
	e := c.store.InTx(ctx, p.FamilyID, func(tx Tx) error {
		var u domain.User
		if e := tx.Get("users", p.UserID, &u); e != nil {
			return E("permission_denied", "Account no longer exists")
		}
		var e error
		result, e = fn(tx, p)
		return e
	})
	return result, e
}
func productInfo(description string, write bool) ActionInfo {
	p := "bags:read"
	if write {
		p = "bags:write"
	}
	return ActionInfo{Description: description, Mutation: write, Permission: p, Web: true, MCP: true}
}
func (c *Core) registerFinanceActions() {
	Register(c, "get_bag_preferences", productInfo("Get the current user's personal bag pins, in pin order. Pins do not change shared bags.", false), c.getBagPreferences)
	Register(c, "set_bag_pin", productInfo("Keep a bag at the top of the current user's list, or unpin it. Set pinned explicitly; reuse request_id on retries.", true), c.setBagPin)
	Register(c, "list_bags", productInfo("List family bags and exact USD-cent balances. Archived bags retain their balances.", false), c.listBags)
	Register(c, "get_bag", productInfo("Get a bag and its current balance in USD cents.", false), c.getBag)
	Register(c, "create_bag", productInfo("Create a bag, optionally with a signed initial amount in USD cents. Reuse request_id when retrying.", true), c.createBag)
	Register(c, "update_bag", productInfo("Rename or describe a bag using its expected_version. Reuse request_id on retries.", true), c.updateBag)
	Register(c, "archive_bag", productInfo("Archive a bag without losing history or balance. Reuse request_id on retries.", true), func(ctx context.Context, p ArchiveBagParams) (any, error) { return c.archiveBag(ctx, p, true) })
	Register(c, "unarchive_bag", productInfo("Unarchive a bag so entries may be created and edited. Reuse request_id on retries.", true), func(ctx context.Context, p ArchiveBagParams) (any, error) { return c.archiveBag(ctx, p, false) })
	Register(c, "delete_bag", productInfo("Permanently delete a bag and all its entries, notes, history and attachments for the family. Requires expected_version. Reuse request_id on retries.", true), c.deleteBag)
	Register(c, "list_entries", productInfo("List dated entries; positive cents add money, negative cents spend, and zero is a note.", false), c.listEntries)
	Register(c, "get_entry", productInfo("Get an entry, notes, version and attachment metadata; file bytes are retrieved separately.", false), c.getEntry)
	Register(c, "get_entry_history", productInfo("Get change history. Historical attachment metadata does not guarantee old file bytes remain available.", false), c.entryHistory)
	Register(c, "create_entry", productInfo("Record one signed integer USD-cent amount in one bag. Zero is allowed. Reuse request_id for retries. Supply files only when their bytes or downloadable references are available.", true), c.createEntry)
	Register(c, "update_entry", productInfo("Edit an entry with expected_version; omitted fields stay unchanged. Attachments add/remove independently. Reuse request_id for retries.", true), c.updateEntry)
	Register(c, "delete_entry", productInfo("Permanently delete an entry, notes, history and attachments. Reuse request_id for retries.", true), c.deleteEntry)
	c.registerAttachmentActions()
	for _, name := range []string{"create_entry", "update_entry"} {
		action := c.actions[name]
		action.info.MaxPayloadBytes = 30 << 20
		c.actions[name] = action
	}
}
func pageBounds(p PageParams, n int) (int, int, string, error) {
	limit := p.Limit
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 {
		return 0, 0, "", E("validation_error", "limit must be between 1 and 100")
	}
	start := 0
	var e error
	if p.Cursor != "" {
		start, e = strconv.Atoi(p.Cursor)
		if e != nil || start < 0 {
			return 0, 0, "", E("validation_error", "Invalid cursor")
		}
	}
	if start > n {
		start = n
	}
	end := start + limit
	next := ""
	if end < n {
		next = strconv.Itoa(end)
	} else {
		end = n
	}
	return start, end, next, nil
}
func checkedVersion(expected, actual int64, current any) error {
	if expected <= 0 {
		return E("validation_error", "expected_version is required")
	}
	if expected != actual {
		return &Error{Code: "version_conflict", Message: "The record changed; reload before editing", Current: current}
	}
	return nil
}
func loadBag(tx Tx, id string, active bool) (domain.Bag, error) {
	var b domain.Bag
	e := tx.Get("bags", id, &b)
	if e != nil {
		return b, e
	}
	if active && b.Archived {
		return b, E("bag_archived", "Unarchive this bag before creating or editing entries")
	}
	return b, nil
}
func familyZone(tx Tx, familyID string) (string, error) {
	var f domain.Family
	if e := tx.Get("families", familyID, &f); e != nil {
		return "", e
	}
	if f.TimeZone == "" {
		return "UTC", nil
	}
	return f.TimeZone, nil
}
func validateBag(tx Tx, b domain.Bag) error {
	if b.NormalizedName == "" || len(b.Name) > 120 {
		return E("validation_error", "Bag name is required and must be at most 120 bytes")
	}
	if len(b.Description) > 4096 {
		return E("validation_error", "Description exceeds 4096 bytes")
	}
	bags, e := listRecords[domain.Bag](tx, "bags")
	if e != nil {
		return e
	}
	for _, old := range bags {
		if old.ID != b.ID && old.NormalizedName == b.NormalizedName {
			return E("validation_error", "A bag with this name already exists, including archived bags")
		}
	}
	return nil
}
func (c *Core) bagResult(tx Tx, b domain.Bag) (BagResult, error) {
	n, e := tx.Balance(b.ID)
	return BagResult{Bag: b, BalanceCents: n, CalculatedAt: c.now()}, e
}
func pageQuery(p PageParams) (offset, limit int, err error) {
	limit = p.Limit
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 {
		return 0, 0, E("validation_error", "limit must be between 1 and 100")
	}
	if p.Cursor != "" {
		offset, err = strconv.Atoi(p.Cursor)
		if err != nil || offset < 0 {
			return 0, 0, E("validation_error", "Invalid cursor")
		}
	}
	return offset, limit, nil
}
func (c *Core) listBags(ctx context.Context, p ListBagsParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		offset, limit, e := pageQuery(p.PageParams)
		if e != nil {
			return nil, e
		}
		q, ok := tx.(QueryTx)
		if !ok {
			return nil, E("internal_error", "Store does not support bounded queries")
		}
		rows, e := q.QueryBags(p.Archived, offset, limit+1)
		if e != nil {
			return nil, e
		}
		next := ""
		if len(rows) > limit {
			next = strconv.Itoa(offset + limit)
			rows = rows[:limit]
		}
		out := make([]BagResult, 0, len(rows))
		now := c.now()
		for _, v := range rows {
			out = append(out, BagResult{Bag: v.Bag, BalanceCents: v.BalanceCents, CalculatedAt: now})
		}
		return map[string]any{"bags": out, "next_cursor": next}, nil
	})
}
func (c *Core) getBag(ctx context.Context, p GetBagParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		b, e := loadBag(tx, p.BagID, false)
		if e != nil {
			return nil, e
		}
		return c.bagResult(tx, b)
	})
}
func (c *Core) createBag(ctx context.Context, p CreateBagParams) (any, error) {
	return c.mutate(ctx, "create_bag", p.RequestID, p, nil, func(tx Tx, actor Principal) (any, string, []string, error) {
		now := c.now()
		b := domain.Bag{ID: c.id(), FamilyID: actor.FamilyID, Name: strings.TrimSpace(p.Name), NormalizedName: domain.NormalizeName(p.Name), Description: p.Description, Version: 1, CreatedAt: now, UpdatedAt: now}
		if e := validateBag(tx, b); e != nil {
			return nil, "", nil, e
		}
		if p.InitialAmountCents != nil && !domain.ValidCents(*p.InitialAmountCents) {
			return nil, "", nil, E("validation_error", "Amount exceeds exact integer range")
		}
		if e := tx.Put("bags", b.ID, b); e != nil {
			return nil, "", nil, e
		}
		if p.InitialAmountCents != nil {
			zone, e := familyZone(tx, actor.FamilyID)
			if e != nil {
				return nil, "", nil, e
			}
			date, e := domain.ValidateDate("", now, zone)
			if e != nil {
				return nil, "", nil, e
			}
			entry, e := c.insertEntry(tx, actor, b.ID, *p.InitialAmountCents, date, "", nil)
			if e != nil {
				return nil, "", nil, e
			}
			_ = entry
		}
		r, e := c.bagResult(tx, b)
		return r, b.ID, nil, e
	})
}
func (c *Core) updateBag(ctx context.Context, p UpdateBagParams) (any, error) {
	return c.mutate(ctx, "update_bag", p.RequestID, p, nil, func(tx Tx, _ Principal) (any, string, []string, error) {
		b, e := loadBag(tx, p.BagID, false)
		if e != nil {
			return nil, "", nil, e
		}
		if e = checkedVersion(p.ExpectedVersion, b.Version, b); e != nil {
			return nil, "", nil, e
		}
		if p.Name != nil {
			b.Name = strings.TrimSpace(*p.Name)
			b.NormalizedName = domain.NormalizeName(*p.Name)
		}
		if p.Description != nil {
			b.Description = *p.Description
		}
		if e = validateBag(tx, b); e != nil {
			return nil, "", nil, e
		}
		b.Version++
		b.UpdatedAt = c.now()
		if e = tx.Put("bags", b.ID, b); e != nil {
			return nil, "", nil, e
		}
		r, e := c.bagResult(tx, b)
		return r, b.ID, nil, e
	})
}
func (c *Core) archiveBag(ctx context.Context, p ArchiveBagParams, archive bool) (any, error) {
	action := "archive_bag"
	if !archive {
		action = "unarchive_bag"
	}
	return c.mutate(ctx, action, p.RequestID, p, nil, func(tx Tx, _ Principal) (any, string, []string, error) {
		b, e := loadBag(tx, p.BagID, false)
		if e != nil {
			return nil, "", nil, e
		}
		if e = checkedVersion(p.ExpectedVersion, b.Version, b); e != nil {
			return nil, "", nil, e
		}
		b.Archived = archive
		b.Version++
		b.UpdatedAt = c.now()
		if e = tx.Put("bags", b.ID, b); e != nil {
			return nil, "", nil, e
		}
		r, e := c.bagResult(tx, b)
		return r, b.ID, nil, e
	})
}
func (c *Core) entryResult(tx Tx, entry domain.Entry) (EntryResult, error) {
	files, e := entryAttachments(tx, entry.ID)
	if e != nil {
		return EntryResult{}, e
	}
	n, e := tx.Balance(entry.BagID)
	return EntryResult{Entry: entry, Attachments: publicAttachments(files), BalanceCents: n, CalculatedAt: c.now()}, e
}
func (c *Core) listEntries(ctx context.Context, p ListEntriesParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		offset, limit, e := pageQuery(p.PageParams)
		if e != nil {
			return nil, e
		}
		for _, date := range []string{p.FromDate, p.ToDate} {
			if date != "" {
				if parsed, e := time.Parse("2006-01-02", date); e != nil || parsed.Format("2006-01-02") != date {
					return nil, E("validation_error", "Date filters must use YYYY-MM-DD")
				}
			}
		}
		if p.BagID != "" {
			if _, e := loadBag(tx, p.BagID, false); e != nil {
				return nil, e
			}
		}
		q, ok := tx.(QueryTx)
		if !ok {
			return nil, E("internal_error", "Store does not support bounded queries")
		}
		entries, e := q.QueryEntries(EntryQuery{BagID: p.BagID, FromDate: p.FromDate, ToDate: p.ToDate, Offset: offset, Limit: limit + 1})
		if e != nil {
			return nil, e
		}
		next := ""
		if len(entries) > limit {
			next = strconv.Itoa(offset + limit)
			entries = entries[:limit]
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, entry := range entries {
			if !seen[entry.BagID] {
				ids = append(ids, entry.BagID)
				seen[entry.BagID] = true
			}
		}
		balances, e := q.Balances(ids)
		if e != nil {
			return nil, e
		}
		out := make([]EntryResult, 0, len(entries))
		now := c.now()
		for _, entry := range entries {
			files, e := entryAttachments(tx, entry.ID)
			if e != nil {
				return nil, e
			}
			out = append(out, EntryResult{Entry: entry, Attachments: publicAttachments(files), BalanceCents: balances[entry.BagID], CalculatedAt: now})
		}
		return map[string]any{"entries": out, "next_cursor": next}, nil
	})
}
func (c *Core) getEntry(ctx context.Context, p GetEntryParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		var entry domain.Entry
		if e := tx.Get("entries", p.EntryID, &entry); e != nil {
			return nil, e
		}
		return c.entryResult(tx, entry)
	})
}
func (c *Core) entryHistory(ctx context.Context, p EntryPageParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		var entry domain.Entry
		if e := tx.Get("entries", p.EntryID, &entry); e != nil {
			return nil, e
		}
		rows, e := listRecords[domain.Revision](tx, "revisions")
		if e != nil {
			return nil, e
		}
		out := make([]domain.Revision, 0)
		for _, r := range rows {
			if r.EntryID == entry.ID {
				out = append(out, r)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
		start, end, next, e := pageBounds(p.PageParams, len(out))
		if e != nil {
			return nil, e
		}
		return map[string]any{"revisions": out[start:end], "next_cursor": next, "attachment_history_notice": "History preserves metadata only; removed files are no longer available."}, nil
	})
}
func (c *Core) insertEntry(tx Tx, actor Principal, bagID string, amount int64, date, notes string, uploads []string) (domain.Entry, error) {
	var author domain.User
	if e := tx.Get("users", actor.UserID, &author); e != nil {
		return domain.Entry{}, e
	}
	now := c.now()
	entry := domain.Entry{ID: c.id(), FamilyID: actor.FamilyID, BagID: bagID, AmountCents: amount, Date: date, Notes: notes, AuthorID: author.ID, AuthorName: author.DisplayName, Version: 1, CreatedAt: now, UpdatedAt: now}
	if entry.AuthorName == "" {
		entry.AuthorName = author.Username
	}
	if e := tx.Put("entries", entry.ID, entry); e != nil {
		return entry, e
	}
	if _, e := tx.Balance(entry.BagID); e != nil {
		return entry, E("validation_error", "Resulting balance exceeds exact integer range")
	}
	if e := c.linkUploads(tx, actor, entry.ID, uploads); e != nil {
		return entry, e
	}
	return entry, c.revise(tx, actor, nil, entry)
}
func (c *Core) createEntry(ctx context.Context, p CreateEntryParams) (any, error) {
	uploads := append([]string(nil), p.AttachmentUploadIDs...)
	return c.mutate(ctx, "create_entry", p.RequestID, p, func() error {
		ids, e := c.prepareFiles(ctx, "create_entry:"+p.RequestID, p.Files)
		uploads = append(uploads, ids...)
		return e
	}, func(tx Tx, actor Principal) (any, string, []string, error) {
		if p.AmountCents == nil {
			return nil, "", nil, E("validation_error", "amount_cents is required; zero is valid")
		}
		if !domain.ValidCents(*p.AmountCents) {
			return nil, "", nil, E("validation_error", "Amount exceeds exact integer range")
		}
		if e := domain.ValidateNotes(p.Notes); e != nil {
			return nil, "", nil, E("validation_error", e.Error())
		}
		if _, e := loadBag(tx, p.BagID, true); e != nil {
			return nil, "", nil, e
		}
		zone, e := familyZone(tx, actor.FamilyID)
		if e != nil {
			return nil, "", nil, e
		}
		date, e := domain.ValidateDate(p.Date, c.now(), zone)
		if e != nil {
			return nil, "", nil, E("validation_error", e.Error())
		}
		entry, e := c.insertEntry(tx, actor, p.BagID, *p.AmountCents, date, p.Notes, uploads)
		if e != nil {
			return nil, "", nil, e
		}
		r, e := c.entryResult(tx, entry)
		return r, entry.ID, nil, e
	})
}
func (c *Core) revise(tx Tx, actor Principal, previous *domain.EntrySnapshot, entry domain.Entry) error {
	files, e := entryAttachments(tx, entry.ID)
	if e != nil {
		return e
	}
	r := domain.Revision{ID: fmt.Sprintf("%s:%d", entry.ID, entry.Version), FamilyID: entry.FamilyID, EntryID: entry.ID, Version: entry.Version, ActorID: actor.UserID, Source: actor.Source, CreatedAt: c.now(), Previous: previous, Current: domain.EntrySnapshot{Entry: entry, Attachments: publicAttachments(files)}}
	return tx.Put("revisions", r.ID, r)
}
func (c *Core) updateEntry(ctx context.Context, p UpdateEntryParams) (any, error) {
	uploads := append([]string(nil), p.AttachmentUploadIDs...)
	return c.mutate(ctx, "update_entry", p.RequestID, p, func() error {
		ids, e := c.prepareFiles(ctx, "update_entry:"+p.RequestID, p.Files)
		uploads = append(uploads, ids...)
		return e
	}, func(tx Tx, actor Principal) (any, string, []string, error) {
		var entry domain.Entry
		if e := tx.Get("entries", p.EntryID, &entry); e != nil {
			return nil, "", nil, e
		}
		if e := checkedVersion(p.ExpectedVersion, entry.Version, entry); e != nil {
			return nil, "", nil, e
		}
		if _, e := loadBag(tx, entry.BagID, true); e != nil {
			return nil, "", nil, e
		}
		files, e := entryAttachments(tx, entry.ID)
		if e != nil {
			return nil, "", nil, e
		}
		before := domain.EntrySnapshot{Entry: entry, Attachments: publicAttachments(files)}
		if p.AmountCents != nil {
			if !domain.ValidCents(*p.AmountCents) {
				return nil, "", nil, E("validation_error", "Amount exceeds exact integer range")
			}
			entry.AmountCents = *p.AmountCents
		}
		if p.Notes != nil {
			if e := domain.ValidateNotes(*p.Notes); e != nil {
				return nil, "", nil, E("validation_error", e.Error())
			}
			entry.Notes = *p.Notes
		}
		if p.Date != nil {
			zone, e := familyZone(tx, actor.FamilyID)
			if e != nil {
				return nil, "", nil, e
			}
			date, e := domain.ValidateDate(*p.Date, c.now(), zone)
			if e != nil {
				return nil, "", nil, E("validation_error", e.Error())
			}
			entry.Date = date
		}
		entry.Version++
		entry.UpdatedAt = c.now()
		if e = tx.Put("entries", entry.ID, entry); e != nil {
			return nil, "", nil, e
		}
		if _, e = tx.Balance(entry.BagID); e != nil {
			return nil, "", nil, E("validation_error", "Resulting balance exceeds exact integer range")
		}
		removed := make([]string, 0)
		seen := map[string]bool{}
		for _, id := range p.RemoveAttachmentIDs {
			if seen[id] {
				return nil, "", nil, E("validation_error", "Attachment ID repeated")
			}
			seen[id] = true
			var file domain.Attachment
			if e = tx.Get("attachments", id, &file); e != nil || file.EntryID != entry.ID {
				return nil, "", nil, ErrNotFound
			}
			if e = tx.Delete("attachments", id); e != nil {
				return nil, "", nil, e
			}
			removed = append(removed, file.BlobKey)
		}
		if e = c.linkUploads(tx, actor, entry.ID, uploads); e != nil {
			return nil, "", nil, e
		}
		if e = c.revise(tx, actor, &before, entry); e != nil {
			return nil, "", nil, e
		}
		r, e := c.entryResult(tx, entry)
		return r, entry.ID, removed, e
	})
}
func (c *Core) deleteEntry(ctx context.Context, p DeleteEntryParams) (any, error) {
	return c.mutate(ctx, "delete_entry", p.RequestID, p, nil, func(tx Tx, _ Principal) (any, string, []string, error) {
		var entry domain.Entry
		if e := tx.Get("entries", p.EntryID, &entry); e != nil {
			return nil, "", nil, e
		}
		if e := checkedVersion(p.ExpectedVersion, entry.Version, entry); e != nil {
			return nil, "", nil, e
		}
		removed, e := deleteEntries(tx, map[string]bool{entry.ID: true})
		if e != nil {
			return nil, "", nil, e
		}
		balance, e := tx.Balance(entry.BagID)
		return map[string]any{"deleted": true, "entry_id": entry.ID, "bag_id": entry.BagID, "balance_cents": balance, "calculated_at": c.now()}, entry.ID, removed, e
	})
}
