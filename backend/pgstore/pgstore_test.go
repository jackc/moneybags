package pgstore_test

import (
	"context"
	"github.com/jackc/moneybags/backend/pgstore"
	"github.com/jackc/moneybags/backend/storetest"
	"os"
	"testing"
)

func TestConformance(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	s, e := pgstore.Open(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	storetest.Run(t, s)
	storetest.RunAuth(t, s)
}
