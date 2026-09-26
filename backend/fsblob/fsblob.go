// Package fsblob implements core.BlobStore on a local directory — the
// default attachment storage (decision 023). Keys map to file paths
// under the root; writes are atomic (temp file + rename) so readers
// never observe a partial value.
package fsblob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/moneybags/backend/core"
)

// Store is a BlobStore rooted at a directory. The zero value is not
// usable; construct with New.
type Store struct {
	root string
}

// New returns a Store rooted at dir, creating it if needed.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("fsblob: directory is required")
	}
	if err := mkdirAllDurable(dir, 0o755); err != nil {
		return nil, fmt.Errorf("fsblob: create root: %w", err)
	}
	return &Store{root: dir}, nil
}

// mkdirAllDurable creates path and fsyncs each new directory plus its parent.
// Syncing the parent is what makes each new directory entry survive a power
// loss; syncing only a file eventually placed in the leaf is not sufficient.
func mkdirAllDurable(path string, perm fs.FileMode) error {
	var missing []string
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: current, Err: errors.New("not a directory")}
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	// missing was collected leaf-first. Sync root-first so each child's
	// directory entry is made durable only after its parent exists durably.
	for i := len(missing) - 1; i >= 0; i-- {
		if err := syncDir(missing[i]); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(missing[i])); err != nil {
			return err
		}
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		dir.Close()
		return err
	}
	return dir.Close()
}

// path validates a key and maps it to a file path. Core mints keys
// from UUIDs and fixed words, so this is a backstop, not a parser:
// anything but simple lowercase segments is rejected outright, which
// makes path escape structurally impossible.
func (s *Store) path(key string) (string, error) {
	if key == "" {
		return "", errors.New("fsblob: empty key")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || !isPlainSegment(segment) {
			return "", fmt.Errorf("fsblob: malformed key %q", key)
		}
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

func isPlainSegment(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (s *Store) PutBlob(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.path(key); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	directory := filepath.Dir(filepath.FromSlash(key))
	if err := root.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("fsblob: create parent: %w", err)
	}
	// Resolve every operation through os.Root so symlink replacement cannot
	// turn an otherwise valid opaque key into a path outside attachment storage.
	parent, err := root.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".put-" + hex.EncodeToString(nonce[:])
	tmp, err := parent.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer parent.Remove(temporary)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = parent.Rename(temporary, filepath.Base(key)); err != nil {
		return err
	}
	// Persist the new file and all newly-created directory entries root-first.
	paths := []string{"."}
	current := ""
	if directory != "." {
		for _, part := range strings.Split(directory, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, name := range paths {
		dir, err := root.Open(name)
		if err != nil {
			return err
		}
		if err := dir.Sync(); err != nil {
			dir.Close()
			return err
		}
		if err := dir.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetBlob(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.path(key); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.FromSlash(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fsblob: read attachment: %w", err)
	}
	return data, nil
}
func (s *Store) DeleteBlob(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.path(key); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.FromSlash(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("fsblob: remove attachment: %w", err)
	}
	// A missing parent means the blob is already absent.
	dir, err := root.Open(filepath.Dir(filepath.FromSlash(key)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// ListBlobs is used only by core's administrative cleanup, never by readers.
func (s *Store) ListBlobs(ctx context.Context) ([]core.BlobInfo, error) {
	var blobs []core.BlobInfo
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// Temporary writes cannot be referenced by metadata. Leave them for
		// an operator when inspecting a interrupted write, never follow links.
		if entry.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		blobs = append(blobs, core.BlobInfo{Key: filepath.ToSlash(key), ModifiedAt: info.ModTime()})
		return nil
	})
	return blobs, err
}
