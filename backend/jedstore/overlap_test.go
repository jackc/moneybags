package jedstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/jackc/moneybags/backend/jedstore"
	"os"
	"os/exec"
	"testing"
	"time"
)

func overlapCore(s *jedstore.Store) *core.Core {
	return core.New(core.Config{Store: s, Origin: "http://localhost:8080", RPID: "localhost", Clock: func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }})
}
func overlapWrites(s *jedstore.Store, name string) error {
	c := overlapCore(s)
	ctx := core.WithPrincipal(context.Background(), core.Principal{UserID: "user", FamilyID: "overlap", Source: "web"})
	for i := -1; i < 20; i++ {
		key := fmt.Sprintf("%s-%d", name, i)
		amount := int64(-1)
		if i == -1 {
			key = "shared-request"
			amount = 100
		}
		raw, _ := json.Marshal(core.CreateEntryParams{RequestID: key, BagID: "bag", AmountCents: &amount})
		if _, e := c.InvokeJSON(ctx, "create_entry", raw); e != nil {
			return fmt.Errorf("%s: %w", key, e)
		}
	}
	return nil
}
func TestProcessOverlap(t *testing.T) {
	dir := t.TempDir()
	s, e := jedstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.CreateFamily(ctx, "overlap"); e != nil {
		t.Fatal(e)
	}
	if e = s.InTx(ctx, "overlap", func(tx core.Tx) error {
		if e := tx.Put("families", "overlap", domain.Family{ID: "overlap", TimeZone: "UTC"}); e != nil {
			return e
		}
		if e := tx.Put("users", "user", domain.User{ID: "user", FamilyID: "overlap", Username: "user"}); e != nil {
			return e
		}
		return tx.Put("bags", "bag", domain.Bag{ID: "bag", FamilyID: "overlap", Name: "Bag", NormalizedName: "bag", Version: 1})
	}); e != nil {
		t.Fatal(e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	type worker struct {
		cmd    *exec.Cmd
		output *os.File
	}
	workers := []worker{}
	for i := 0; i < 2; i++ {
		f, e := os.CreateTemp(t.TempDir(), "worker")
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		cmd := exec.Command(executable, "-test.run=^TestProcessOverlapHelper$")
		cmd.Env = append(os.Environ(), "MONEYBAGS_OVERLAP_DIR="+dir, fmt.Sprintf("MONEYBAGS_OVERLAP_NAME=child%d", i))
		cmd.Stdout = f
		cmd.Stderr = f
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		workers = append(workers, worker{cmd, f})
	}
	parentErr := overlapWrites(s, "parent")
	for _, w := range workers {
		if e := w.cmd.Wait(); e != nil {
			data, _ := os.ReadFile(w.output.Name())
			t.Errorf("child: %v: %s", e, data)
		}
	}
	if parentErr != nil {
		t.Fatal(parentErr)
	}
	if e = s.InTx(ctx, "overlap", func(tx core.Tx) error {
		sum, e := tx.Balance("bag")
		if e != nil {
			return e
		}
		if sum != 40 {
			return fmt.Errorf("cross-process balance %d; want 40 (one shared credit and 60 debits)", sum)
		}
		rows, e := tx.List("entries")
		if e != nil {
			return e
		}
		if len(rows) != 61 {
			return fmt.Errorf("cross-process entries %d; want 61", len(rows))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestProcessOverlapHelper(t *testing.T) {
	dir := os.Getenv("MONEYBAGS_OVERLAP_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	s, e := jedstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = overlapWrites(s, os.Getenv("MONEYBAGS_OVERLAP_NAME")); e != nil {
		t.Fatal(e)
	}
}
func TestMissingFamilyDoesNotRecreateFile(t *testing.T) {
	dir := t.TempDir()
	s, e := jedstore.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.InTx(context.Background(), "missing", func(tx core.Tx) error { return nil })
	if e == nil {
		t.Fatal("missing family accepted")
	}
	ids, e := s.FamilyIDs(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(ids) != 0 {
		t.Fatalf("missing read created family files: %v", ids)
	}
}
