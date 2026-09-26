// Package pgstore implements family-scoped persistence through PostgreSQL's checked tenant views.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/jackc/moneybags/db/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
	"io/fs"
	"math/big"
	"strings"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	pool, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	s := &Store{pool: pool}
	if e = s.Ping(ctx); e != nil {
		pool.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error                   { s.pool.Close(); return nil }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *Store) Migrate(ctx context.Context) error {
	conn, e := s.pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	m, e := migrate.NewMigrator(ctx, conn.Conn(), "public.schema_version")
	if e != nil {
		return e
	}
	files, e := fs.Sub(migrations.FS, "postgresql")
	if e != nil {
		return e
	}
	if e = m.LoadMigrations(files); e != nil {
		return e
	}
	return m.Migrate(ctx)
}
func (s *Store) InTx(ctx context.Context, family string, fn func(core.Tx) error) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 917))", family); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "SELECT set_config('app.family_id',$1,true)", family); e != nil {
		return e
	}
	if e = fn(&transaction{ctx: ctx, tx: tx, family: family}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type transaction struct {
	ctx    context.Context
	tx     pgx.Tx
	family string
}

func table(kind string) (string, bool) {
	switch kind {
	case "bags", "entries", "attachments":
		return kind, true
	default:
		return "records", false
	}
}
func (t *transaction) checkScope(kind string) error {
	_, financial := table(kind)
	if financial && t.family == "" {
		return core.E("permission_denied", "Family scope is required")
	}
	return nil
}
func (t *transaction) Get(kind, id string, out any) error {
	if e := t.checkScope(kind); e != nil {
		return e
	}
	tab, typed := table(kind)
	query := "SELECT data FROM " + tab + " WHERE family_id=$1 AND id=$2"
	args := []any{t.family, id}
	if !typed {
		query += " AND kind=$3"
		args = append(args, kind)
	}
	var raw string
	if e := t.tx.QueryRow(t.ctx, query, args...).Scan(&raw); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return core.ErrNotFound
		}
		return e
	}
	return json.Unmarshal([]byte(raw), out)
}
func (t *transaction) List(kind string) ([]json.RawMessage, error) {
	if e := t.checkScope(kind); e != nil {
		return nil, e
	}
	tab, typed := table(kind)
	query := "SELECT data FROM " + tab + " WHERE family_id=$1"
	args := []any{t.family}
	if !typed {
		query += " AND kind=$2"
		args = append(args, kind)
	}
	query += " ORDER BY id"
	rows, e := t.tx.Query(t.ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]json.RawMessage, 0)
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
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
	tab, typed := table(kind)
	columns := []string{"family_id", "id", "data"}
	args := []any{t.family, id, string(raw)}
	if !typed {
		columns = append(columns, "kind")
		args = append(args, kind)
	}
	switch kind {
	case "bags":
		var b domain.Bag
		if e = json.Unmarshal(raw, &b); e != nil {
			return e
		}
		columns = append(columns, "normalized_name", "archived")
		args = append(args, b.NormalizedName, b.Archived)
	case "entries":
		var v domain.Entry
		if e = json.Unmarshal(raw, &v); e != nil {
			return e
		}
		columns = append(columns, "bag_id", "amount_cents", "entry_date", "created_at")
		args = append(args, v.BagID, v.AmountCents, v.Date, v.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z"))
	case "attachments":
		var a domain.Attachment
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
		columns = append(columns, "entry_id")
		args = append(args, a.EntryID)
	}
	set := make([]string, 0)
	for i := 2; i < len(columns); i++ {
		set = append(set, fmt.Sprintf("%s=$%d", columns[i], i+1))
	}
	where := "family_id=$1 AND id=$2"
	if !typed {
		where += " AND kind=$4"
	}
	result, e := t.tx.Exec(t.ctx, "UPDATE "+tab+" SET "+strings.Join(set, ",")+" WHERE "+where, args...)
	if e != nil {
		return e
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	_, e = t.tx.Exec(t.ctx, "INSERT INTO "+tab+" ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(placeholders, ",")+")", args...)
	return e
}
func (t *transaction) Delete(kind, id string) error {
	if e := t.checkScope(kind); e != nil {
		return e
	}
	tab, typed := table(kind)
	query := "DELETE FROM " + tab + " WHERE family_id=$1 AND id=$2"
	args := []any{t.family, id}
	if !typed {
		query += " AND kind=$3"
		args = append(args, kind)
	}
	_, e := t.tx.Exec(t.ctx, query, args...)
	return e
}
func (t *transaction) Balance(bagID string) (int64, error) {
	if t.family == "" {
		return 0, core.E("permission_denied", "Family scope is required")
	}
	var sum string
	if e := t.tx.QueryRow(t.ctx, "SELECT COALESCE(SUM(amount_cents),0)::text FROM entries WHERE family_id=$1 AND bag_id=$2", t.family, bagID).Scan(&sum); e != nil {
		return 0, e
	}
	n, ok := new(big.Int).SetString(sum, 10)
	if !ok || !n.IsInt64() || !domain.ValidCents(n.Int64()) {
		return 0, core.E("validation_error", "Balance exceeds exact integer range")
	}
	return n.Int64(), nil
}
func (s *Store) FamilyIDs(ctx context.Context) ([]string, error) {
	rows, e := s.pool.Query(ctx, "SELECT DISTINCT family_id FROM all_records WHERE family_id<>'' ORDER BY family_id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
