package site

import (
	"html"
	"net/url"
	"strings"
	"testing"
)

func TestEmptyPages(t *testing.T) {
	index, err := Index(nil)
	if err != nil || !strings.Contains(string(index), "Фотоархив пустой.") {
		t.Fatal("empty archive template")
	}
	album, err := Album("Пустой", nil)
	if err != nil || !strings.Contains(string(album), "Фотоальбом пустой.") || strings.Contains(string(album), "Galleria.run") {
		t.Fatal("empty album template")
	}
	if !strings.Contains(string(Error()), `href="index.html"`) {
		t.Fatal("error page has no return link")
	}
}

func TestEscapingPreservesPhotoURL(t *testing.T) {
	name := `Фото + % # ? & "<tag>".JPG`
	key := "albums/61/photos/" + name
	page, err := Album(`<script>alert("x")</script>`, []Photo{{Name: name, Key: key}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	if strings.Contains(s, `<script>alert`) || strings.Contains(s, `<tag>`) {
		t.Fatal("unescaped HTML")
	}
	start := strings.Index(s, `<img src="`)
	if start < 0 {
		t.Fatal("no image")
	}
	src := strings.SplitN(s[start+len(`<img src="`):], `"`, 2)[0]
	decoded, err := url.PathUnescape(html.UnescapeString(src))
	if err != nil || decoded != key {
		t.Fatalf("URL did not roundtrip: %q", decoded)
	}
	parsed, parseErr := url.Parse(html.UnescapeString(src))
	if parseErr != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatal("filename changed into query or fragment")
	}
	if !strings.Contains(s, "galleria/1.6.1") || !strings.Contains(s, "jquery/3.6.0") {
		t.Fatal("required gallery dependencies missing")
	}
}
