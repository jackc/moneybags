package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/moneybags/backend/domain"
)

type StageAttachmentParams struct {
	RequestID   string `json:"request_id"`
	FileName    string `json:"file_name"`
	MIMEType    string `json:"mime_type,omitempty"`
	Data        []byte `json:"data,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	FileID      string `json:"file_id,omitempty"`
}
type StageAttachmentResult struct {
	UploadID   string            `json:"upload_id"`
	ExpiresAt  time.Time         `json:"expires_at"`
	Attachment domain.Attachment `json:"attachment"`
}
type GetAttachmentParams struct {
	AttachmentID string `json:"attachment_id"`
}
type AttachmentResult struct {
	domain.Attachment
	Data []byte `json:"data"`
}
type CleanupAttachmentsParams struct {
	FamilyID     string `json:"family_id"`
	GraceSeconds int    `json:"grace_seconds,omitempty"`
}

func (c *Core) registerAttachmentActions() {
	info := productInfo("Stage bounded bytes or a downloadable file for one later entry. Reuse request_id on retries. Files expire after 24 hours.", true)
	info.MaxPayloadBytes = 8 << 20
	Register(c, "stage_attachment", info, c.stageAttachment)
	Register(c, "list_attachments", productInfo("List entry attachment metadata, without file bytes or download URLs.", false), c.listAttachments)
	Register(c, "get_attachment", productInfo("Retrieve one authorized attachment's bytes and metadata. Active content must be downloaded.", false), c.getAttachment)
	Register(c, "cleanup_attachments", ActionInfo{Description: "Remove expired staged files and orphan blobs after a grace period.", Mutation: true, Administrative: true}, c.cleanupAttachments)
}
func publicAttachment(a domain.Attachment) domain.Attachment { a.BlobKey = ""; return a }
func publicAttachments(files []domain.Attachment) []domain.Attachment {
	out := make([]domain.Attachment, 0, len(files))
	for _, a := range files {
		out = append(out, publicAttachment(a))
	}
	return out
}
func entryAttachments(tx Tx, entryID string) ([]domain.Attachment, error) {
	if q, ok := tx.(QueryTx); ok {
		return q.EntryAttachments(entryID)
	}
	files, e := listRecords[domain.Attachment](tx, "attachments")
	if e != nil {
		return nil, e
	}
	out := make([]domain.Attachment, 0)
	for _, a := range files {
		if a.EntryID == entryID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
func detectedMIME(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf"
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}):
		return "image/png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case utf8.Valid(data) && !bytes.Contains(data, []byte{0}):
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
func (c *Core) stageAttachment(ctx context.Context, p StageAttachmentParams) (any, error) {
	var data []byte
	var mimeType, blobKey, fileName string
	var attachmentID string
	return c.mutate(ctx, "stage_attachment", p.RequestID, p, func() error {
		if c.blobs == nil {
			return E("attachment_unavailable", "Attachment storage is unavailable")
		}
		if len(p.Data) > 0 && p.DownloadURL != "" {
			return E("validation_error", "Provide bytes or a download URL, not both")
		}
		if p.DownloadURL != "" {
			if p.FileID == "" {
				return E("validation_error", "file_id is required with a download URL")
			}
			if c.fetcher == nil {
				return E("attachment_unavailable", "File downloads are unavailable")
			}
			var e error
			data, _, e = c.fetcher.Fetch(ctx, p.DownloadURL)
			if e != nil {
				return E("attachment_unavailable", "Could not retrieve the file; refresh its download URL and retry")
			}
		} else {
			data = p.Data
		}
		if len(data) > domain.MaxFileBytes {
			return E("validation_error", "Each attachment must be at most 5 MiB")
		}
		if data == nil {
			return E("validation_error", "File bytes or a downloadable file reference are required")
		}
		fileName = path.Base(strings.ReplaceAll(p.FileName, "\\", "/"))
		if fileName == "." || fileName == "/" || fileName == "" || len(fileName) > 255 || strings.ContainsAny(fileName, "\r\n\x00") {
			return E("validation_error", "A valid file_name is required")
		}
		mimeType = detectedMIME(data)
		actor, _ := PrincipalFromContext(ctx)
		attachmentID = c.id()
		blobKey = actor.FamilyID + "/" + attachmentID
		if e := c.blobs.PutBlob(ctx, blobKey, data); e != nil {
			return E("attachment_unavailable", "Could not store attachment bytes; retry the same request")
		}
		return nil
	}, func(tx Tx, actor Principal) (any, string, []string, error) {
		files, e := listRecords[domain.Attachment](tx, "attachments")
		if e != nil {
			return nil, "", nil, e
		}
		uploads, e := listRecords[domain.Upload](tx, "uploads")
		if e != nil {
			return nil, "", nil, e
		}
		size := int64(len(data))
		for _, a := range files {
			size += a.Size
		}
		for _, u := range uploads {
			if u.ConsumedEntryID == "" {
				size += u.Attachment.Size
			}
		}
		if size > c.quota {
			return nil, "", nil, E("validation_error", "Family attachment storage quota exceeded")
		}
		hash := sha256.Sum256(data)
		now := c.now()
		a := domain.Attachment{ID: attachmentID, FamilyID: actor.FamilyID, BlobKey: blobKey, FileName: fileName, MIMEType: mimeType, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), CreatedAt: now}
		upload := domain.Upload{ID: c.id(), FamilyID: actor.FamilyID, ActorID: actor.UserID, Attachment: a, ExpiresAt: now.Add(24 * time.Hour)}
		if e = tx.Put("uploads", upload.ID, upload); e != nil {
			return nil, "", nil, e
		}
		return StageAttachmentResult{UploadID: upload.ID, ExpiresAt: upload.ExpiresAt, Attachment: publicAttachment(a)}, upload.ID, nil, nil
	})
}
func (c *Core) prepareFiles(ctx context.Context, requestID string, files []FileSource) ([]string, error) {
	ids := make([]string, 0, len(files))
	var total int64
	for i, file := range files {
		if file.DownloadURL != "" && file.FileID == "" {
			return nil, E("validation_error", "file_id is required for each downloadable file")
		}
		if file.FileName == "" {
			file.FileName = "attachment"
		}
		key := sha256.Sum256([]byte(fmt.Sprintf("%s:file:%d", requestID, i)))
		r, e := c.stageAttachment(ctx, StageAttachmentParams{RequestID: "internal-file-" + hex.EncodeToString(key[:]), FileName: file.FileName, MIMEType: file.MIMEType, Data: file.Data, DownloadURL: file.DownloadURL, FileID: file.FileID})
		if e != nil {
			return nil, e
		}
		switch r := r.(type) {
		case StageAttachmentResult:
			total += r.Attachment.Size
			ids = append(ids, r.UploadID)
		case map[string]any:
			if r["deleted"] == true {
				return nil, E("attachment_unavailable", "The staged attachment belonged to an entry that has been deleted")
			}
			if metadata, ok := r["attachment"].(map[string]any); ok {
				if size, ok := metadata["size"].(float64); ok {
					total += int64(size)
				}
			}
			id, ok := r["upload_id"].(string)
			if !ok {
				return nil, E("attachment_unavailable", "Staged attachment is no longer available")
			}
			ids = append(ids, id)
		default:
			return nil, E("attachment_unavailable", "Staging returned an invalid result")
		}
		if total > 20<<20 {
			return nil, E("validation_error", "File inputs exceed the 20 MiB request limit")
		}
	}
	return ids, nil
}
func (c *Core) linkUploads(tx Tx, actor Principal, entryID string, ids []string) error {
	for _, id := range ids {
		var upload domain.Upload
		if e := tx.Get("uploads", id, &upload); e != nil {
			return e
		}
		if upload.ActorID != actor.UserID || upload.FamilyID != actor.FamilyID {
			return ErrNotFound
		}
		if upload.ConsumedEntryID != "" {
			return E("validation_error", "Upload has already been attached; stage a separate copy for another entry")
		}
		if !c.now().Before(upload.ExpiresAt) {
			return E("attachment_unavailable", "Upload expired; stage the file again")
		}
		upload.ConsumedEntryID = entryID
		a := upload.Attachment
		a.EntryID = entryID
		if e := tx.Put("attachments", a.ID, a); e != nil {
			return e
		}
		if e := tx.Put("uploads", id, upload); e != nil {
			return e
		}
	}
	return nil
}
func (c *Core) listAttachments(ctx context.Context, p EntryPageParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		var entry domain.Entry
		if e := tx.Get("entries", p.EntryID, &entry); e != nil {
			return nil, e
		}
		files, e := entryAttachments(tx, entry.ID)
		if e != nil {
			return nil, e
		}
		start, end, next, e := pageBounds(p.PageParams, len(files))
		if e != nil {
			return nil, e
		}
		return map[string]any{"attachments": publicAttachments(files[start:end]), "next_cursor": next}, nil
	})
}
func (c *Core) getAttachment(ctx context.Context, p GetAttachmentParams) (any, error) {
	return c.read(ctx, func(tx Tx, _ Principal) (any, error) {
		var a domain.Attachment
		if e := tx.Get("attachments", p.AttachmentID, &a); e != nil {
			return nil, e
		}
		var entry domain.Entry
		if e := tx.Get("entries", a.EntryID, &entry); e != nil {
			return nil, e
		}
		if c.blobs == nil {
			return nil, E("attachment_unavailable", "Attachment storage is unavailable")
		}
		data, e := c.blobs.GetBlob(ctx, a.BlobKey)
		if e != nil {
			return nil, E("attachment_unavailable", "Attachment bytes are no longer available")
		}
		return AttachmentResult{Attachment: publicAttachment(a), Data: data}, nil
	})
}
func (c *Core) cleanupAttachments(ctx context.Context, p CleanupAttachmentsParams) (any, error) {
	if p.FamilyID == "" {
		return nil, E("validation_error", "family_id is required")
	}
	if c.blobs == nil {
		return nil, E("attachment_unavailable", "Attachment storage is unavailable")
	}
	grace := time.Duration(p.GraceSeconds) * time.Second
	if grace < time.Hour {
		grace = time.Hour
	}
	now := c.now()
	refs := map[string]bool{}
	removed := []string{}
	e := c.store.InTx(ctx, p.FamilyID, func(tx Tx) error {
		files, e := listRecords[domain.Attachment](tx, "attachments")
		if e != nil {
			return e
		}
		for _, a := range files {
			refs[a.BlobKey] = true
		}
		uploads, e := listRecords[domain.Upload](tx, "uploads")
		if e != nil {
			return e
		}
		for _, u := range uploads {
			if u.ConsumedEntryID == "" && !now.Before(u.ExpiresAt.Add(grace)) {
				if e = tx.Delete("uploads", u.ID); e != nil {
					return e
				}
				removed = append(removed, u.Attachment.BlobKey)
			} else if u.ConsumedEntryID == "" {
				refs[u.Attachment.BlobKey] = true
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if lister, ok := c.blobs.(BlobLister); ok {
		blobs, e := lister.ListBlobs(ctx)
		if e != nil {
			return nil, e
		}
		for _, b := range blobs {
			if strings.HasPrefix(b.Key, p.FamilyID+"/") && !refs[b.Key] && b.ModifiedAt.Before(now.Add(-grace)) {
				removed = append(removed, b.Key)
			}
		}
	}
	count := 0
	seen := map[string]bool{}
	for _, key := range removed {
		if seen[key] {
			continue
		}
		seen[key] = true
		if e = c.blobs.DeleteBlob(ctx, key); e != nil && !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		count++
	}
	return map[string]any{"deleted_blobs": count}, nil
}
