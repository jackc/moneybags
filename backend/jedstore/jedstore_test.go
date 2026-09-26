package jedstore_test

import (
	"github.com/jackc/moneybags/backend/jedstore"
	"github.com/jackc/moneybags/backend/storetest"
	"testing"
)

func TestConformance(t *testing.T) {
	s, e := jedstore.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	storetest.Run(t, s)
	storetest.RunAuth(t, s)
}
