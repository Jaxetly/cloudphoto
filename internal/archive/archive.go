package archive

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"cloudphoto/internal/storage"
)

var ErrAlbum = errors.New("В фотоархиве нет альбома с именем ")

type Archive struct{ Store storage.Store }
type Photo struct{ Name, Key string }

func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsAny(name, "/\x00")
}

func albumPrefix(album string) (string, error) {
	if !validName(album) {
		return "", errors.New("Ошибка в аргументах")
	}
	prefix := "albums/" + hex.EncodeToString([]byte(album)) + "/"
	if len(prefix)+len(".album") > 1024 {
		return "", errors.New("Имя альбома слишком длинное")
	}
	return prefix, nil
}

func (a *Archive) exists(ctx context.Context, prefix string) error {
	body, err := a.Store.Get(ctx, prefix+".album")
	if errors.Is(err, storage.ErrNotFound) {
		return ErrAlbum
	}
	if err != nil {
		return err
	}
	return body.Close()
}

func (a *Archive) Albums(ctx context.Context) ([]string, error) {
	keys, err := a.Store.List(ctx, "albums/")
	if err != nil {
		return nil, err
	}
	var albums []string
	for _, key := range keys {
		encoded, found := strings.CutSuffix(strings.TrimPrefix(key, "albums/"), "/.album")
		if !found || strings.Contains(encoded, "/") {
			continue
		}
		name, err := hex.DecodeString(encoded)
		if err != nil || !validName(string(name)) || hex.EncodeToString(name) != encoded {
			continue
		}
		albums = append(albums, string(name))
	}
	sort.Strings(albums)
	return albums, nil
}

func (a *Archive) Photos(ctx context.Context, album string) ([]Photo, error) {
	prefix, err := albumPrefix(album)
	if err != nil {
		return nil, err
	}
	if err := a.exists(ctx, prefix); err != nil {
		return nil, err
	}
	prefix += "photos/"
	keys, err := a.Store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var photos []Photo
	for _, key := range keys {
		name, found := strings.CutPrefix(key, prefix)
		if found && validName(name) && storage.IsPhoto(name) {
			photos = append(photos, Photo{Name: name, Key: key})
		}
	}
	sort.Slice(photos, func(i, j int) bool { return photos[i].Name < photos[j].Name })
	return photos, nil
}

func (a *Archive) Upload(ctx context.Context, album, dir string) error {
	prefix, err := albumPrefix(album)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("Каталог фотографий недоступен")
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !storage.IsPhoto(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("Фотография должна быть обычным доступным файлом")
		}
		if !validName(entry.Name()) || len(prefix+"photos/"+entry.Name()) > 1024 {
			return errors.New("Недопустимое имя фотографии")
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return errors.New("В каталоге нет фотографий")
	}
	if err := a.Store.Put(ctx, prefix+".album", bytes.NewReader(nil), 0, "application/octet-stream"); err != nil {
		return err
	}
	for _, name := range names {
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errors.New("Не удалось прочитать фотографию")
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			return errors.New("Не удалось прочитать фотографию")
		}
		err = a.Store.Put(ctx, prefix+"photos/"+name, file, info.Size(), "image/jpeg")
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return errors.New("Ошибка чтения фотографии")
		}
	}
	return nil
}
