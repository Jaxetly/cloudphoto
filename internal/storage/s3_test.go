package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

func TestCredentialsAndEndpointAreExplicit(t *testing.T) {
	t.Setenv("AWS_SESSION_TOKEN", "ignored-session")
	t.Setenv("AWS_REGION", "wrong-region")
	t.Setenv("AWS_ENDPOINT_URL", "https://wrong.invalid")
	s := New("test.example", "test-access", "test-secret")
	options := s.client.Options()
	creds, err := options.Credentials.Retrieve(context.Background())
	if err != nil || creds.AccessKeyID != "test-access" || creds.SecretAccessKey != "test-secret" || creds.SessionToken != "" {
		t.Fatal("unexpected credential source")
	}
	if options.Region != "ru-central1" || *options.BaseEndpoint != "https://storage.yandexcloud.net" || !options.UsePathStyle {
		t.Fatal("unexpected endpoint settings")
	}
}

func TestErrorsDoNotExposeServerPayload(t *testing.T) {
	err := safeError(&smithy.GenericAPIError{Code: "AccessDenied", Message: "test-secret signed-url"})
	if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "signed-url") {
		t.Fatal("server message leaked")
	}
}

func TestPhotoExtensions(t *testing.T) {
	for _, name := range []string{"a.jpg", "a.JPEG", "Фото.JpEg", ".jpg"} {
		if !IsPhoto(name) {
			t.Fatal("valid extension rejected")
		}
	}
	for _, name := range []string{"a.png", "jpg", "a.jpg.txt", "a.jpg "} {
		if IsPhoto(name) {
			t.Fatal("invalid extension accepted")
		}
	}
}
