// Package jedstore stores global routing/authentication separately from each family's authoritative transactional data.
package jedstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	jed "github.com/jackc/jed/impl/go"
	migrate "github.com/jackc/jed/migrate/go"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/jackc/moneybags/db/migrations"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Store struct {
	dir   string
	mu    sync.Mutex
	files map[string]*jed.Database
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(filepath.Join(dir, "families"), 0700); e != nil {
		return nil, e
	}
	s := &Store{dir: dir, files: map[string]*jed.Database{}}
	if _, e := s.database(""); e != nil {
		return nil, e
	}
	return s, nil
}
func validFamilyID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (s *Store) database(family string) (*jed.Database, error) { return s.openDatabase(family, false) }
func (s *Store) CreateFamily(ctx context.Context, family string) error {
	if family == "" {
		return core.E("validation_error", "Family identifier is required")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	_, e := s.openDatabase(family, true)
	return e
}
func (s *Store) openDatabase(family string, create bool) (*jed.Database, error) {
	if !validFamilyID(family) {
		return nil, core.E("validation_error", "Invalid family identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if db := s.files[family]; db != nil {
		return db, nil
	}
	filename := filepath.Join(s.dir, "global.jed")
	root := "jed/global"
	if family != "" {
		filename = filepath.Join(s.dir, "families", family+".jed")
		root = "jed/family"
	}
	var db *jed.Database
	var e error
	if _, e = os.Stat(filename); errors.Is(e, os.ErrNotExist) {
		if family != "" && !create {
			return nil, core.ErrNotFound
		}
		db, e = jed.CreateDatabase(jed.CreateOptions{Path: filename})
		if e != nil {
			if _, statErr := os.Stat(filename); statErr == nil {
				db, e = jed.OpenDatabase(filename)
			}
		}
	} else if e == nil {
		db, e = jed.OpenDatabase(filename)
	}
	if e != nil {
		return nil, e
	}
	steps, e := migrate.LoadMigrationsFS(migrations.FS, root)
	if e != nil {
		db.Close()
		return nil, e
	}
	m, e := migrate.NewMigrator(db, steps, migrate.Options{})
	if e != nil {
		db.Close()
		return nil, e
	}
	e = m.Migrate()
	m.Close()
	if e != nil {
		db.Close()
		return nil, e
	}
	s.files[family] = db
	return db, nil
}
func (s *Store) InTx(ctx context.Context, family string, fn func(core.Tx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	db, e := s.database(family)
	if e != nil {
		return e
	}
	return db.Update(func(tx *jed.Transaction) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := fn(&transaction{ctx: ctx, tx: tx, family: family}); e != nil {
			return e
		}
		return ctx.Err()
	})
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for family, db := range s.files {
		if e := db.Close(); e != nil && first == nil {
			first = e
		}
		delete(s.files, family)
	}
	return first
}
func (s *Store) Ping(ctx context.Context) error {
	db, e := s.database("")
	if e != nil {
		return e
	}
	return db.QueryRow(ctx, "SELECT 1").Scan(new(int64))
}
func (s *Store) FamilyIDs(ctx context.Context) ([]string, error) {
	entries, e := os.ReadDir(filepath.Join(s.dir, "families"))
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jed") {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".jed"))
		}
	}
	sort.Strings(ids)
	return ids, ctx.Err()
}

type transaction struct {
	ctx    context.Context
	tx     *jed.Transaction
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
		if errors.Is(e, jed.ErrNoRows) {
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
	if count, _ := result.RowsAffected(); count > 0 {
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
	var sum jed.Decimal
	if e := t.tx.QueryRow(t.ctx, "SELECT COALESCE(SUM(CAST(amount_cents AS numeric)),CAST(0 AS numeric)) FROM entries WHERE family_id=$1 AND bag_id=$2", t.family, bagID).Scan(&sum); e != nil {
		return 0, e
	}
	n, ok := new(big.Int).SetString(sum.Render(), 10)
	if !ok || !n.IsInt64() || !domain.ValidCents(n.Int64()) {
		return 0, core.E("validation_error", "Balance exceeds exact integer range")
	}
	return n.Int64(), nil
}
