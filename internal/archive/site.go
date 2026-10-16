package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cloudphoto/internal/site"
)

func (a *Archive) MakeSite(ctx context.Context) error {
	albums, err := a.Albums(ctx)
	if err != nil {
		return err
	}
	type page struct {
		key  string
		body []byte
	}
	pages := make([]page, 0, len(albums)+2)
	links := make([]site.AlbumLink, 0, len(albums))
	for i, album := range albums {
		photos, err := a.Photos(ctx, album)
		if err != nil {
			return err
		}
		images := make([]site.Photo, len(photos))
		for j, p := range photos {
			images[j] = site.Photo{Name: p.Name, Key: p.Key}
		}
		body, err := site.Album(album, images)
		if err != nil {
			return errors.New("Не удалось сформировать страницу альбома")
		}
		key := fmt.Sprintf("album%d.html", i+1)
		pages = append(pages, page{key, body})
		links = append(links, site.AlbumLink{Name: album, Page: key})
	}
	index, err := site.Index(links)
	if err != nil {
		return errors.New("Не удалось сформировать главную страницу")
	}
	pages = append(pages, page{"error.html", site.Error()}, page{"index.html", index})
	if err := a.Store.Publish(ctx); err != nil {
		return err
	}
	for _, p := range pages {
		if err := a.Store.Put(ctx, p.key, bytes.NewReader(p.body), int64(len(p.body)), "text/html; charset=utf-8"); err != nil {
			return err
		}
	}
	return nil
}
