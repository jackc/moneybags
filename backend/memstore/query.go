package memstore

import (
	"encoding/json"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"sort"
)

func (t *transaction) QueryBags(archived *bool, offset, limit int) ([]core.BagBalance, error) {
	rows, e := t.List("bags")
	if e != nil {
		return nil, e
	}
	out := []core.BagBalance{}
	for _, raw := range rows {
		var b domain.Bag
		if e = json.Unmarshal(raw, &b); e != nil {
			return nil, e
		}
		if archived != nil && b.Archived != *archived {
			continue
		}
		sum, e := t.Balance(b.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, core.BagBalance{Bag: b, BalanceCents: sum})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Bag, out[j].Bag
		if a.NormalizedName == b.NormalizedName {
			return a.ID < b.ID
		}
		return a.NormalizedName < b.NormalizedName
	})
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (t *transaction) QueryEntries(q core.EntryQuery) ([]domain.Entry, error) {
	rows, e := t.List("entries")
	if e != nil {
		return nil, e
	}
	out := []domain.Entry{}
	for _, raw := range rows {
		var v domain.Entry
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		if (q.BagID == "" || q.BagID == v.BagID) && (q.FromDate == "" || v.Date >= q.FromDate) && (q.ToDate == "" || v.Date <= q.ToDate) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Date != b.Date {
			return a.Date > b.Date
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
	offset := q.Offset
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + q.Limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (t *transaction) Balances(ids []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, id := range ids {
		sum, e := t.Balance(id)
		if e != nil {
			return nil, e
		}
		out[id] = sum
	}
	return out, nil
}
func (t *transaction) EntryAttachments(id string) ([]domain.Attachment, error) {
	rows, e := t.List("attachments")
	if e != nil {
		return nil, e
	}
	out := []domain.Attachment{}
	for _, raw := range rows {
		var v domain.Attachment
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		if v.EntryID == id {
			out = append(out, v)
		}
	}
	return out, nil
}
