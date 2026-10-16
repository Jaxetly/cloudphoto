package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestArguments(t *testing.T) {
	for _, argv := range [][]string{nil, {"unknown"}, {"upload"}, {"download", "--album"},
		{"list", "--path", "."}, {"mksite", "--album", "a"}, {"list", "extra"},
		{"list", "--album="}, {"delete", "--album=a", "--photo="},
		{"upload", "--album=a", "--album=b"}, {"list", "--album=a/b"}} {
		if _, err := Parse(argv); !errors.Is(err, ErrArguments) {
			t.Fatalf("accepted invalid arguments: %q", argv)
		}
	}
	for _, argv := range [][]string{{"list"}, {"mksite"}, {"upload", "--path=.", "--album", "  Отпуск & <лето>  "},
		{"download", "--album=--dash"}, {"delete", "--photo=A.JpEg", "--album=a"}} {
		if _, err := Parse(argv); err != nil {
			t.Fatalf("rejected valid arguments: %q", argv)
		}
	}
}

func TestEnvironmentCheckedBeforeExecution(t *testing.T) {
	for _, missing := range []string{"BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		var out, errout bytes.Buffer
		code := Run(context.Background(), []string{"unknown"}, func(name string) string {
			if name == missing {
				return ""
			}
			return "test-value"
		}, &out, &errout, func(context.Context, Config, Args) ([]string, error) {
			t.Fatal("execution with missing environment")
			return nil, nil
		})
		if code != 1 || out.Len() != 0 || errout.String() != "Не заданы переменные окружения\n" {
			t.Fatal("invalid missing-environment response")
		}
	}
}

func TestStreamsAndExitStatus(t *testing.T) {
	var out, errout bytes.Buffer
	env := func(string) string { return "test-value" }
	code := Run(context.Background(), []string{"list"}, env, &out, &errout,
		func(context.Context, Config, Args) ([]string, error) { return []string{"A", "a", "Фото"}, nil })
	if code != 0 || out.String() != "A\na\nФото\n" || errout.Len() != 0 {
		t.Fatal("incorrect successful output")
	}
	out.Reset()
	errout.Reset()
	code = Run(context.Background(), []string{"upload"}, env, &out, &errout,
		func(context.Context, Config, Args) ([]string, error) {
			t.Fatal("invalid arguments reached execution")
			return nil, nil
		})
	if code != 1 || out.Len() != 0 || errout.String() != "Ошибка в аргументах\n" {
		t.Fatal("incorrect argument error")
	}
}
