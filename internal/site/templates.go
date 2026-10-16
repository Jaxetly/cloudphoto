package site

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
)

type AlbumLink struct{ Name, Page string }
type Photo struct{ Name, Key string }
type image struct{ Name, URL string }

func render(source string, value any) ([]byte, error) {
	t, err := template.New("page").Parse(source)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := t.Execute(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func Index(albums []AlbumLink) ([]byte, error) { return render(indexTemplate, albums) }
func Error() []byte                            { return []byte(errorTemplate) }

func Album(name string, photos []Photo) ([]byte, error) {
	images := make([]image, len(photos))
	for i, p := range photos {
		parts := strings.Split(p.Key, "/")
		for j := range parts {
			parts[j] = url.PathEscape(parts[j])
		}
		images[i] = image{Name: p.Name, URL: strings.Join(parts, "/")}
	}
	return render(albumTemplate, struct {
		Name   string
		Images []image
	}{name, images})
}

const indexTemplate = `<!doctype html>
<html>
    <head>
        <meta charset="utf-8">
        <title>Фотоархив</title>
    </head>
<body>
    <h1>Фотоархив</h1>
    {{if .}}<ul>
        {{range .}}<li><a href="{{.Page}}">{{.Name}}</a></li>
        {{end}}
    </ul>{{else}}<p>Фотоархив пустой.</p>{{end}}
</body>
</html>
`

const albumTemplate = `<!doctype html>
<html>
    <head>
        <meta charset="utf-8">
        <title>Фотоальбом "{{.Name}}"</title>
        {{if .Images}}<link rel="stylesheet" type="text/css" href="https://cdnjs.cloudflare.com/ajax/libs/galleria/1.6.1/themes/classic/galleria.classic.min.css" />
        <style>
            .galleria{ width: 960px; height: 540px; background: #000 }
        </style>
        <script src="https://ajax.googleapis.com/ajax/libs/jquery/3.6.0/jquery.min.js"></script>
        <script src="https://cdnjs.cloudflare.com/ajax/libs/galleria/1.6.1/galleria.min.js"></script>
        <script src="https://cdnjs.cloudflare.com/ajax/libs/galleria/1.6.1/themes/classic/galleria.classic.min.js"></script>{{end}}
    </head>
    <body>
        <h1>Фотоальбом "{{.Name}}"</h1>
        {{if .Images}}<div class="galleria">
            {{range .Images}}<img src="{{.URL}}" data-title="{{.Name}}">
            {{end}}
        </div>{{else}}<p>Фотоальбом пустой.</p>{{end}}
        <p>Вернуться на <a href="index.html">главную страницу</a> фотоархива</p>
        {{if .Images}}<script>
            (function() {
                Galleria.run('.galleria');
            }());
        </script>{{end}}
    </body>
</html>
`

const errorTemplate = `<!doctype html>
<html>
    <head>
        <meta charset="utf-8">
        <title>Фотоархив</title>
    </head>
<body>
    <h1>Ошибка</h1>
    <p>Ошибка при доступе к фотоархиву. Вернитесь на <a href="index.html">главную страницу</a> фотоархива.</p>
</body>
</html>
`
