package memstore_test

import (
	"github.com/jackc/moneybags/backend/memstore"
	"github.com/jackc/moneybags/backend/storetest"
	"testing"
)

func TestConformance(t *testing.T) { s := memstore.New(); storetest.Run(t, s); storetest.RunAuth(t, s) }
