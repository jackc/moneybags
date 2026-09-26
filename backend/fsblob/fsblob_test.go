package fsblob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/moneybags/backend/core"
)

func TestOpaqueBlobLifecycleAndIndependentCopies(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"family-one/attachments/file-one", "family-two/attachments/file-two"} {
		if err := store.PutBlob(ctx, key, []byte("same receipt")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteBlob(ctx, "family-one/attachments/file-one"); err != nil {
		t.Fatal(err)
	}
	bytes, err := store.GetBlob(ctx, "family-two/attachments/file-two")
	if err != nil || string(bytes) != "same receipt" {
		t.Fatalf("independent copy altered: %q %v", bytes, err)
	}
	if _, err := store.GetBlob(ctx, "family-one/attachments/file-one"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing error %v", err)
	}
	if err := store.DeleteBlob(ctx, "family-one/attachments/file-one"); err != nil {
		t.Fatal(err)
	}
	files, err := store.ListBlobs(ctx)
	if err != nil || len(files) != 1 || files[0].Key != "family-two/attachments/file-two" {
		t.Fatalf("list %+v %v", files, err)
	}
}
func TestBlobRejectsPathsAndSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"", "/tmp/file", "../file", "family/../file", "family//file", "family/file.jpg", "family\\file", "family/%2fsecret"} {
		if err := store.PutBlob(ctx, key, []byte("secret")); err == nil {
			t.Errorf("accepted %q", key)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "family")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetBlob(ctx, "family/secret"); err == nil {
		t.Fatal("read escaped blob root")
	}
	if err := store.PutBlob(ctx, "family/new", []byte("secret")); err == nil {
		t.Fatal("write escaped blob root")
	}
	if err := store.DeleteBlob(ctx, "family/secret"); err == nil {
		t.Fatal("delete escaped blob root")
	}
	if data, err := os.ReadFile(filepath.Join(outside, "secret")); err != nil || string(data) != "outside" {
		t.Fatalf("outside file altered %q %v", data, err)
	}
}
func TestBlobRespectsCancellation(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.PutBlob(ctx, "family/file", []byte("receipt")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.GetBlob(ctx, "family/file"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.DeleteBlob(ctx, "family/file"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
