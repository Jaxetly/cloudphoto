package archive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"cloudphoto/internal/storage"
)

func (a *Archive) Download(ctx context.Context, album, dir string) error {
	photos, err := a.Photos(ctx, album)
	if err != nil {
		return err
	}
	// Пустой альбом тоже требует доступной для записи директории.
	probe, err := os.CreateTemp(dir, ".cloudphoto-")
	if err != nil {
		return errors.New("Каталог недоступен для сохранения фотографий")
	}
	probe.Close()
	os.Remove(probe.Name())
	for _, photo := range photos {
		if err := a.downloadPhoto(ctx, photo, dir); err != nil {
			return err
		}
	}
	return nil
}

func (a *Archive) downloadPhoto(ctx context.Context, photo Photo, dir string) error {
	if !validName(photo.Name) {
		return errors.New("Недопустимое имя фотографии в хранилище")
	}
	body, err := a.Store.Get(ctx, photo.Key)
	if err != nil {
		return err
	}
	defer body.Close()
	file, err := os.CreateTemp(dir, ".cloudphoto-")
	if err != nil {
		return errors.New("Не удалось создать файл фотографии")
	}
	defer os.Remove(file.Name())
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return errors.New("Не удалось сохранить фотографию полностью")
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, photo.Name)); err != nil {
		return errors.New("Не удалось заменить файл фотографии")
	}
	return nil
}

func (a *Archive) Delete(ctx context.Context, album, photo string) error {
	prefix, err := albumPrefix(album)
	if err != nil {
		return err
	}
	if photo != "" {
		if !validName(photo) {
			return errors.New("Ошибка в аргументах")
		}
		if err := a.exists(ctx, prefix); err != nil {
			return err
		}
		key := prefix + "photos/" + photo
		if !storage.IsPhoto(photo) {
			return errors.New("В альбоме нет такой фотографии")
		}
		body, err := a.Store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return errors.New("В альбоме нет такой фотографии")
		}
		if err != nil {
			return err
		}
		body.Close()
		return a.Store.Delete(ctx, []string{key})
	}
	photos, err := a.Photos(ctx, album)
	if err != nil {
		return err
	}
	keys := make([]string, len(photos))
	for i, p := range photos {
		keys[i] = p.Key
	}
	if err := a.Store.Delete(ctx, keys); err != nil {
		return err
	}
	// Маркер удаляется после фотографий: при частичном сбое альбом можно повторить.
	return a.Store.Delete(ctx, []string{prefix + ".album"})
}
