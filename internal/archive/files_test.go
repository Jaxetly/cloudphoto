package archive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadAndDeleteLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	src := t.TempDir()
	dst := t.TempDir()
	write(t, src, "A.JPG", "new contents")
	write(t, dst, "A.JPG", "old contents")
	if err := a.Upload(ctx, "a", src); err != nil {
		t.Fatal(err)
	}
	if err := a.Upload(ctx, "ab", src); err != nil {
		t.Fatal(err)
	}
	if err := a.Download(ctx, "a", dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "A.JPG"))
	if err != nil || string(b) != "new contents" {
		t.Fatal("download did not replace file")
	}
	if err := a.Delete(ctx, "a", "a.jpg"); err == nil {
		t.Fatal("case-insensitive deletion")
	}
	if err := a.Delete(ctx, "a", "A.JPG"); err != nil {
		t.Fatal(err)
	}
	photos, err := a.Photos(ctx, "a")
	if err != nil || len(photos) != 0 {
		t.Fatal("last photo deletion removed album")
	}
	if err := a.Download(ctx, "a", dst); err != nil {
		t.Fatal("empty album download failed")
	}
	if err := a.Delete(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Photos(ctx, "a"); !errors.Is(err, ErrAlbum) {
		t.Fatal("album marker not removed")
	}
	if photos, err := a.Photos(ctx, "ab"); err != nil || len(photos) != 1 {
		t.Fatal("neighbour album damaged")
	}
	if err := a.Delete(ctx, "a", ""); err == nil {
		t.Fatal("repeated delete must fail")
	}
	if err := a.Download(ctx, "missing", dst); err == nil {
		t.Fatal("missing album accepted")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("interrupted") }
func (brokenReader) Close() error             { return nil }

type brokenDownload struct {
	*memory
	brokenKey string
}

func (m brokenDownload) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == m.brokenKey {
		return brokenReader{}, nil
	}
	return m.memory.Get(ctx, key)
}

func TestInterruptedDownloadPreservesExistingFile(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	prefix, _ := albumPrefix("a")
	key := prefix + "photos/file.jpg"
	m.objects[prefix+".album"] = nil
	m.objects[key] = []byte("new")
	a := Archive{Store: brokenDownload{m, key}}
	dir := t.TempDir()
	write(t, dir, "file.jpg", "old")
	if err := a.Download(ctx, "a", dir); err == nil {
		t.Fatal("interruption hidden")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "file.jpg"))
	if string(b) != "old" {
		t.Fatal("existing file damaged")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary file was not removed")
	}
}

func TestDownloadDoesNotFollowDestinationSymlink(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	prefix, _ := albumPrefix("a")
	m.objects[prefix+".album"] = nil
	m.objects[prefix+"photos/file.jpg"] = []byte("photo")
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "private.txt", "keep")
	if err := os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(dir, "file.jpg")); err != nil {
		t.Fatal(err)
	}
	a := Archive{Store: m}
	if err := a.Download(ctx, "a", dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(outside, "private.txt"))
	if string(b) != "keep" {
		t.Fatal("wrote through symlink")
	}
	b, _ = os.ReadFile(filepath.Join(dir, "file.jpg"))
	if string(b) != "photo" {
		t.Fatal("photo not saved")
	}
	if err := a.downloadPhoto(ctx, Photo{Name: "../escape.jpg", Key: prefix + "photos/file.jpg"}, dir); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestPartialDeleteKeepsMarkerAndUnknownObjects(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	prefix, _ := albumPrefix("a")
	m.objects[prefix+".album"] = nil
	m.objects[prefix+"photos/a.jpg"] = []byte("photo")
	m.objects[prefix+"notes.txt"] = []byte("keep")
	m.failDelete = prefix + "photos/a.jpg"
	if err := a.Delete(ctx, "a", ""); err == nil {
		t.Fatal("partial delete hidden")
	}
	if _, ok := m.objects[prefix+".album"]; !ok {
		t.Fatal("marker removed after failed delete")
	}
	m.failDelete = ""
	if err := a.Delete(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if string(m.objects[prefix+"notes.txt"]) != "keep" {
		t.Fatal("unknown object removed")
	}
}

func TestInaccessibleDownloadDirectory(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	prefix, _ := albumPrefix("empty")
	m.objects[prefix+".album"] = nil
	a := Archive{Store: m}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := a.Download(ctx, "empty", dir); err == nil {
		t.Fatal("read-only directory accepted")
	}
}
