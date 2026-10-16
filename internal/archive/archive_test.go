package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cloudphoto/internal/storage"
)

type memory struct {
	objects                      map[string][]byte
	failPut, failGet, failDelete string
	published                    bool
	deletes                      [][]string
}

func newMemory() *memory { return &memory{objects: map[string][]byte{}} }
func (m *memory) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for key := range m.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}
func (m *memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if key == m.failGet {
		return nil, errors.New("test read failure")
	}
	b, ok := m.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memory) Put(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	if key == m.failPut {
		return errors.New("test write failure")
	}
	b, err := io.ReadAll(body)
	if err == nil {
		m.objects[key] = b
	}
	return err
}
func (m *memory) Delete(_ context.Context, keys []string) error {
	m.deletes = append(m.deletes, append([]string(nil), keys...))
	for _, key := range keys {
		if key == m.failDelete {
			return errors.New("test delete failure")
		}
		delete(m.objects, key)
	}
	return nil
}
func (m *memory) Publish(context.Context) error { m.published = true; return nil }

func write(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUploadNamesSortingAndReplacement(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	dir := t.TempDir()
	for _, name := range []string{"a.jpg", "A.JPG", "Фото & #.JpEg", "е.jpg", "ё.jpg", "é.jpg", "é.jpg"} {
		write(t, dir, name, name)
	}
	write(t, dir, "ignore.png", "not a photo")
	if err := os.Mkdir(filepath.Join(dir, "nested.jpg"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "nested.jpg"), "hidden.jpg", "nested")
	if err := a.Upload(ctx, "  Альбом <лето>  ", dir); err != nil {
		t.Fatal(err)
	}
	photos, err := a.Photos(ctx, "  Альбом <лето>  ")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range photos {
		names = append(names, p.Name)
	}
	want := []string{"A.JPG", "a.jpg", "é.jpg", "é.jpg", "Фото & #.JpEg", "е.jpg", "ё.jpg"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names: %q", names)
	}
	write(t, dir, "a.jpg", "replacement")
	if err := a.Upload(ctx, "  Альбом <лето>  ", dir); err != nil {
		t.Fatal(err)
	}
	prefix, _ := albumPrefix("  Альбом <лето>  ")
	if string(m.objects[prefix+"photos/a.jpg"]) != "replacement" {
		t.Fatal("photo was not replaced")
	}
	if err := a.Upload(ctx, "Другой", dir); err != nil {
		t.Fatal(err)
	}
	albums, err := a.Albums(ctx)
	if err != nil || !reflect.DeepEqual(albums, []string{"  Альбом <лето>  ", "Другой"}) {
		t.Fatal("album names changed")
	}
}

func TestEmptyAndMissingAlbums(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	albums, err := a.Albums(ctx)
	if err != nil || len(albums) != 0 {
		t.Fatal("empty archive")
	}
	if _, err := a.Photos(ctx, "missing"); !errors.Is(err, ErrAlbum) {
		t.Fatal("missing album was not detected")
	}
	prefix, _ := albumPrefix("empty")
	m.objects[prefix+".album"] = nil
	photos, err := a.Photos(ctx, "empty")
	if err != nil || len(photos) != 0 {
		t.Fatal("empty album lost")
	}
	m.objects["index.html"] = []byte("site")
	m.objects["albums/not-hex/.album"] = nil
	albums, err = a.Albums(ctx)
	if err != nil || !reflect.DeepEqual(albums, []string{"empty"}) {
		t.Fatal("service objects leaked into list")
	}
	m.failGet = prefix + ".album"
	if _, err := a.Photos(ctx, "empty"); err == nil || errors.Is(err, ErrAlbum) {
		t.Fatal("read failure treated as missing album")
	}
}

func TestUploadFailureAndSymlink(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	dir := t.TempDir()
	if err := a.Upload(ctx, "a", dir); err == nil || len(m.objects) != 0 {
		t.Fatal("empty upload accepted")
	}
	write(t, dir, "photo.jpg", "data")
	prefix, _ := albumPrefix("a")
	m.failPut = prefix + "photos/photo.jpg"
	if err := a.Upload(ctx, "a", dir); err == nil {
		t.Fatal("partial failure hidden")
	}
	if _, exists := m.objects[prefix+".album"]; !exists {
		t.Fatal("partial upload has no album marker")
	}
	if err := os.Symlink(filepath.Join(dir, "photo.jpg"), filepath.Join(dir, "link.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := a.Upload(ctx, "b", dir); err == nil {
		t.Fatal("followed symlink")
	}
}
