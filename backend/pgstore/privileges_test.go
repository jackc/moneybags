package pgstore_test

import (
	"context"
	"fmt"
	"github.com/jackc/moneybags/backend/pgstore"
	"github.com/jackc/moneybags/backend/storetest"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestRuntimeUsesOnlyTenantViews(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	owner, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close(ctx)
	migrationStore, e := pgstore.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrationStore.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	migrationStore.Close()
	role := fmt.Sprintf("moneybags_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{role}.Sanitize()
	if _, e = owner.Exec(ctx, "CREATE ROLE "+quoted+" NOLOGIN"); e != nil {
		t.Fatal(e)
	}
	defer func() { owner.Exec(ctx, "DROP OWNED BY "+quoted); owner.Exec(ctx, "DROP ROLE "+quoted) }()
	if _, e = owner.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+quoted); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON records,bags,entries,attachments TO "+quoted); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("options", "-c role="+role)
	u.RawQuery = q.Encode()
	runtime, e := pgstore.Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	storetest.Run(t, runtime)
	storetest.RunAuth(t, runtime)
	restricted, e := pgx.Connect(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer restricted.Close(ctx)
	for _, table := range []string{"all_entries", "all_bags", "all_records", "all_attachments"} {
		if _, e = restricted.Exec(ctx, "SELECT 1 FROM "+table+" LIMIT 1"); e == nil {
			t.Fatalf("runtime can read %s", table)
		}
	}
	if _, e = restricted.Exec(ctx, "SELECT * FROM bags"); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = restricted.QueryRow(ctx, "SELECT count(*) FROM bags").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("missing tenant setting returned bags")
	}
}
