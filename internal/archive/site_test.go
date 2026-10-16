package archive

import (
	"context"
	"strings"
	"testing"
)

func TestSiteNumberingAndRepeatedPublication(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	if err := a.MakeSite(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.published || !strings.Contains(string(m.objects["index.html"]), "Фотоархив пустой.") {
		t.Fatal("empty site not published")
	}
	for _, name := range []string{"a", "A", "Фото"} {
		prefix, _ := albumPrefix(name)
		m.objects[prefix+".album"] = nil
	}
	prefix, _ := albumPrefix("A")
	m.objects[prefix+"photos/Фото #.jpg"] = []byte("photo")
	if err := a.MakeSite(ctx); err != nil {
		t.Fatal(err)
	}
	index := string(m.objects["index.html"])
	if !strings.Contains(index, `href="album1.html">A</a>`) || !strings.Contains(index, `href="album2.html">a</a>`) {
		t.Fatal("album numbering is not byte-sorted")
	}
	if !strings.Contains(string(m.objects["album2.html"]), "Фотоальбом пустой.") {
		t.Fatal("empty album page missing")
	}
	if !strings.Contains(string(m.objects["album1.html"]), "%23.jpg") {
		t.Fatal("photo URL not escaped")
	}
	if err := a.Delete(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.MakeSite(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m.objects["index.html"]), ">a</a>") {
		t.Fatal("stale album link")
	}
}

func TestSiteFailureDoesNotReplaceIndex(t *testing.T) {
	ctx := context.Background()
	m := newMemory()
	a := Archive{Store: m}
	prefix, _ := albumPrefix("a")
	m.objects[prefix+".album"] = nil
	m.objects["index.html"] = []byte("previous index")
	m.failPut = "album1.html"
	if err := a.MakeSite(ctx); err == nil {
		t.Fatal("publishing error hidden")
	}
	if string(m.objects["index.html"]) != "previous index" {
		t.Fatal("index replaced before album publication")
	}
}
