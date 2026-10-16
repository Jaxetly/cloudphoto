package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func testStore(t *testing.T, handler http.HandlerFunc) *S3 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store := New("test.example", "fake-access", "fake-secret")
	options := store.client.Options()
	options.BaseEndpoint = aws.String(server.URL)
	options.HTTPClient = server.Client()
	options.RetryMaxAttempts = 1
	store.client = s3.New(options)
	return store
}

func TestListReadsEveryPage(t *testing.T) {
	requests := 0
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>page-2</NextContinuationToken><Contents><Key>albums/a</Key></Contents></ListBucketResult>`)
		} else {
			io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>albums/b</Key></Contents></ListBucketResult>`)
		}
	})
	keys, err := store.List(context.Background(), "albums/")
	if err != nil || requests != 2 || !reflect.DeepEqual(keys, []string{"albums/a", "albums/b"}) {
		t.Fatal("incomplete listing")
	}
}

func TestBrokenPaginationFailsWithoutPartialResult(t *testing.T) {
	requests := 0
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same</NextContinuationToken><Contents><Key>a</Key></Contents></ListBucketResult>`)
	})
	keys, err := store.List(context.Background(), "")
	if err == nil || keys != nil || requests != 2 {
		t.Fatal("broken pagination accepted")
	}
}

func TestDeleteBatchesAndPerObjectErrors(t *testing.T) {
	var counts []int
	failed := false
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := md5.Sum(body)
		if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(sum[:]) || r.ContentLength != int64(len(body)) {
			t.Error("required Content-MD5 or Content-Length missing")
		}
		var request struct {
			Objects []struct{ Key string } `xml:"Object"`
		}
		if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&request); err != nil {
			t.Error(err)
		}
		counts = append(counts, len(request.Objects))
		w.Header().Set("Content-Type", "application/xml")
		if failed {
			io.WriteString(w, `<DeleteResult><Error><Key>test.jpg</Key><Code>AccessDenied</Code><Message>fake-secret</Message></Error></DeleteResult>`)
		} else {
			io.WriteString(w, `<DeleteResult/>`)
		}
	})
	keys := make([]string, 2001)
	for i := range keys {
		keys[i] = fmt.Sprintf("photo%d.jpg", i)
	}
	if err := store.Delete(context.Background(), keys); err != nil || !reflect.DeepEqual(counts, []int{1000, 1000, 1}) {
		t.Fatal("incorrect delete batching")
	}
	failed = true
	if err := store.Delete(context.Background(), []string{"test.jpg"}); err == nil {
		t.Fatal("partial delete error hidden")
	}
}

func TestPublishOnlyAllowsObjectReads(t *testing.T) {
	var policy string
	website := false
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := md5.Sum(body)
		if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Error("missing configuration checksum")
		}
		if r.URL.Query().Has("policy") {
			policy = string(body)
		}
		if r.URL.Query().Has("website") {
			website = len(body) != 0
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := store.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Statement []struct {
			Action   string
			Resource string
		}
	}
	if err := json.Unmarshal([]byte(policy), &document); err != nil || len(document.Statement) != 1 || document.Statement[0].Action != "s3:GetObject" || document.Statement[0].Resource != "arn:aws:s3:::test.example/*" || !website {
		t.Fatal("unsafe or incomplete publication settings")
	}
}

func TestPublishErrorIdentifiesStepWithoutResponsePayload(t *testing.T) {
	for _, denied := range []string{"policy", "website"} {
		t.Run(denied, func(t *testing.T) {
			store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has(denied) {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusForbidden)
					io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>fake-secret</Message></Error>`)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			err := store.Publish(context.Background())
			if err == nil || strings.Contains(err.Error(), "fake-secret") {
				t.Fatal("unsafe error")
			}
			step := "публичное чтение"
			if denied == "website" {
				step = "хостинг"
			}
			if !strings.Contains(err.Error(), step) {
				t.Fatal("missing operation context")
			}
		})
	}
}
