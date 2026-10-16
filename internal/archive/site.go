package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"

	"cloudphoto/internal/site"
	"cloudphoto/internal/storage"
)

var albumPageKey = regexp.MustCompile(`^album[1-9][0-9]*\.html$`)

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
	var publicPhotos []string
	for i, album := range albums {
		photos, err := a.Photos(ctx, album)
		if err != nil {
			return err
		}
		images := make([]site.Photo, len(photos))
		for j, p := range photos {
			images[j] = site.Photo{Name: p.Name, Key: p.Key}
			publicPhotos = append(publicPhotos, p.Key)
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
	if err := a.Store.Publish(ctx, publicPhotos); err != nil {
		return err
	}
	for _, p := range pages {
		if err := a.Store.PutPublic(ctx, p.key, bytes.NewReader(p.body), int64(len(p.body)), "text/html; charset=utf-8"); err != nil {
			return err
		}
	}
	return a.removeStaleAlbumPages(ctx, links)
}

func (a *Archive) removeStaleAlbumPages(ctx context.Context, links []site.AlbumLink) error {
	keys, err := a.Store.List(ctx, "album")
	if err != nil {
		return err
	}
	current := make(map[string]bool, len(links))
	for _, link := range links {
		current[link.Page] = true
	}
	var stale []string
	for _, key := range keys {
		if current[key] || !albumPageKey.MatchString(key) {
			continue
		}
		body, err := a.Store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		generated, readErr := site.IsAlbumPage(body)
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("Не удалось проверить устаревшую страницу альбома")
		}
		if generated {
			stale = append(stale, key)
		}
	}
	return a.Store.Delete(ctx, stale)
}
