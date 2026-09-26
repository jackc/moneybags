package pgstore

import (
	"encoding/json"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"math/big"
	"strings"
)

func (t *transaction) QueryBags(archived *bool, offset, limit int) ([]core.BagBalance, error) {
	if t.family == "" {
		return nil, core.E("permission_denied", "Family scope is required")
	}
	query := "SELECT b.data, CAST(COALESCE(s.balance,0) AS text) FROM bags AS b LEFT JOIN (SELECT bag_id,SUM(CAST(amount_cents AS numeric)) AS balance FROM entries WHERE family_id=$1 GROUP BY bag_id) AS s ON b.id=s.bag_id WHERE b.family_id=$1"
	args := []any{t.family}
	if archived != nil {
		args = append(args, *archived)
		query += fmt.Sprintf(" AND b.archived=$%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY b.normalized_name,b.id LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := t.tx.Query(t.ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.BagBalance{}
	for rows.Next() {
		var raw, sum string
		if e = rows.Scan(&raw, &sum); e != nil {
			return nil, e
		}
		var b domain.Bag
		if e = json.Unmarshal([]byte(raw), &b); e != nil {
			return nil, e
		}
		n, e := parseBalance(sum)
		if e != nil {
			return nil, e
		}
		out = append(out, core.BagBalance{Bag: b, BalanceCents: n})
	}
	return out, rows.Err()
}
func parseBalance(sum string) (int64, error) {
	n, ok := new(big.Int).SetString(sum, 10)
	if !ok || !n.IsInt64() || !domain.ValidCents(n.Int64()) {
		return 0, core.E("validation_error", "Balance exceeds exact integer range")
	}
	return n.Int64(), nil
}
func (t *transaction) QueryEntries(q core.EntryQuery) ([]domain.Entry, error) {
	if t.family == "" {
		return nil, core.E("permission_denied", "Family scope is required")
	}
	query := "SELECT data FROM entries WHERE family_id=$1"
	args := []any{t.family}
	for _, filter := range []struct{ value, expr string }{{q.BagID, "bag_id="}, {q.FromDate, "entry_date>="}, {q.ToDate, "entry_date<="}} {
		if filter.value != "" {
			args = append(args, filter.value)
			query += fmt.Sprintf(" AND %s$%d", filter.expr, len(args))
		}
	}
	args = append(args, q.Limit, q.Offset)
	query += fmt.Sprintf(" ORDER BY entry_date DESC,created_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := t.tx.Query(t.ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Entry{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var entry domain.Entry
		if e = json.Unmarshal([]byte(raw), &entry); e != nil {
			return nil, e
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
func (t *transaction) Balances(ids []string) (map[string]int64, error) {
	if t.family == "" {
		return nil, core.E("permission_denied", "Family scope is required")
	}
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{t.family}
	placeholders := []string{}
	for _, id := range ids {
		out[id] = 0
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, e := t.tx.Query(t.ctx, "SELECT bag_id,CAST(SUM(CAST(amount_cents AS numeric)) AS text) FROM entries WHERE family_id=$1 AND bag_id IN ("+strings.Join(placeholders, ",")+") GROUP BY bag_id", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, sum string
		if e = rows.Scan(&id, &sum); e != nil {
			return nil, e
		}
		n, e := parseBalance(sum)
		if e != nil {
			return nil, e
		}
		out[id] = n
	}
	return out, rows.Err()
}
func (t *transaction) EntryAttachments(entryID string) ([]domain.Attachment, error) {
	if t.family == "" {
		return nil, core.E("permission_denied", "Family scope is required")
	}
	rows, e := t.tx.Query(t.ctx, "SELECT data FROM attachments WHERE family_id=$1 AND entry_id=$2 ORDER BY id", t.family, entryID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Attachment{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var a domain.Attachment
		if e = json.Unmarshal([]byte(raw), &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
