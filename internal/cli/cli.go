package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrArguments = errors.New("Ошибка в аргументах")

type Args struct{ Command, Album, Photo, Path string }
type Config struct{ Bucket, AccessKey, SecretKey string }
type Execute func(context.Context, Config, Args) ([]string, error)

func ValidName(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsAny(value, "/\x00")
}

func Parse(argv []string) (Args, error) {
	if len(argv) == 0 {
		return Args{}, ErrArguments
	}
	a := Args{Command: argv[0], Path: "."}
	allowed := map[string]string{"list": "album", "upload": "album,path", "download": "album,path", "delete": "album,photo", "mksite": ""}
	options, ok := allowed[a.Command]
	if !ok {
		return Args{}, ErrArguments
	}
	seen := map[string]bool{}
	for i := 1; i < len(argv); i++ {
		if !strings.HasPrefix(argv[i], "--") {
			return Args{}, ErrArguments
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(argv[i], "--"), "=")
		permitted := false
		for _, option := range strings.Split(options, ",") {
			if option != "" && name == option {
				permitted = true
			}
		}
		if !permitted || seen[name] {
			return Args{}, ErrArguments
		}
		seen[name] = true
		if !inline {
			i++
			if i >= len(argv) || strings.HasPrefix(argv[i], "--") {
				return Args{}, ErrArguments
			}
			value = argv[i]
		}
		if value == "" {
			return Args{}, ErrArguments
		}
		switch name {
		case "album":
			if !ValidName(value) {
				return Args{}, ErrArguments
			}
			a.Album = value
		case "photo":
			if !ValidName(value) {
				return Args{}, ErrArguments
			}
			a.Photo = value
		case "path":
			a.Path = value
		}
	}
	if a.Command != "list" && a.Command != "mksite" && a.Album == "" {
		return Args{}, ErrArguments
	}
	return a, nil
}

func Run(ctx context.Context, argv []string, getenv func(string) string, stdout, stderr io.Writer, execute Execute) int {
	config := Config{getenv("BUCKET"), getenv("AWS_ACCESS_KEY_ID"), getenv("AWS_SECRET_ACCESS_KEY")}
	if config.Bucket == "" || config.AccessKey == "" || config.SecretKey == "" {
		fmt.Fprintln(stderr, "Не заданы переменные окружения")
		return 1
	}
	args, err := Parse(argv)
	if err != nil {
		fmt.Fprintln(stderr, ErrArguments)
		return 1
	}
	lines, err := execute(ctx, config, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			fmt.Fprintln(stderr, "Не удалось вывести результат")
			return 1
		}
	}
	return 0
}
