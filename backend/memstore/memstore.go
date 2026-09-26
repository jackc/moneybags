// Package memstore implements a serializable in-memory store for core tests.
package memstore

import (
	"context"
	"encoding/json"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"math/big"
	"sort"
	"sync"
)

type Store struct {
	mu      sync.Mutex
	records map[string]map[string]map[string]json.RawMessage
}

func New() *Store { return &Store{records: map[string]map[string]map[string]json.RawMessage{}} }
func (s *Store) InTx(ctx context.Context, family string, fn func(core.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	copyRecords := map[string]map[string]json.RawMessage{}
	for kind, rows := range s.records[family] {
		copyRecords[kind] = map[string]json.RawMessage{}
		for id, raw := range rows {
			copyRecords[kind][id] = append(json.RawMessage(nil), raw...)
		}
	}
	tx := &transaction{family: family, records: copyRecords}
	if e := fn(tx); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	s.records[family] = copyRecords
	return nil
}

type transaction struct {
	family  string
	records map[string]map[string]json.RawMessage
}

func (t *transaction) checkScope(kind string) error {
	if t.family == "" && (kind == "bags" || kind == "entries" || kind == "attachments") {
		return core.E("permission_denied", "Family scope is required")
	}
	return nil
}
func (t *transaction) Get(kind, id string, out any) error {
	if e := t.checkScope(kind); e != nil {
		return e
	}
	r, ok := t.records[kind][id]
	if !ok {
		return core.ErrNotFound
	}
	return json.Unmarshal(r, out)
}
func (t *transaction) List(kind string) ([]json.RawMessage, error) {
	if e := t.checkScope(kind); e != nil {
		return nil, e
	}
	ids := make([]string, 0, len(t.records[kind]))
	for id := range t.records[kind] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		out = append(out, append(json.RawMessage(nil), t.records[kind][id]...))
	}
	return out, nil
}
func (t *transaction) Put(kind, id string, value any) error {
	if e := t.checkScope(kind); e != nil {
		return e
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	var scope struct {
		FamilyID string `json:"family_id"`
	}
	if e = json.Unmarshal(raw, &scope); e != nil {
		return e
	}
	if t.family != "" && scope.FamilyID != "" && scope.FamilyID != t.family {
		return core.E("permission_denied", "Cross-family reference")
	}
	if kind == "entries" {
		var entry domain.Entry
		if e = json.Unmarshal(raw, &entry); e != nil {
			return e
		}
		var bag domain.Bag
		if e = t.Get("bags", entry.BagID, &bag); e != nil {
			return e
		}
	}
	if kind == "attachments" {
		var a domain.Attachment
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
		var entry domain.Entry
		if e = t.Get("entries", a.EntryID, &entry); e != nil {
			return e
		}
	}
	if t.records[kind] == nil {
		t.records[kind] = map[string]json.RawMessage{}
	}
	t.records[kind][id] = raw
	return nil
}
func (t *transaction) Delete(kind, id string) error {
	if e := t.checkScope(kind); e != nil {
		return e
	}
	delete(t.records[kind], id)
	return nil
}
func (t *transaction) Balance(bagID string) (int64, error) {
	if e := t.checkScope("entries"); e != nil {
		return 0, e
	}
	sum := new(big.Int)
	for _, raw := range t.records["entries"] {
		var e domain.Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return 0, err
		}
		if e.BagID == bagID {
			sum.Add(sum, big.NewInt(e.AmountCents))
		}
	}
	if !sum.IsInt64() || !domain.ValidCents(sum.Int64()) {
		return 0, core.E("validation_error", "Balance exceeds exact integer range")
	}
	return sum.Int64(), nil
}
