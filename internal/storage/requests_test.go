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

func TestPublishKeepsAuthenticatedAccessAndOnlyPublicReads(t *testing.T) {
	var policy string
	website := false
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<Error><Code>NoSuchBucketPolicy</Code></Error>`)
			return
		}
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
			Effect    string
			Principal string
			Action    string
			Resource  json.RawMessage
			Condition map[string]map[string]string
		}
	}
	if err := json.Unmarshal([]byte(policy), &document); err != nil || len(document.Statement) != 3 || !website {
		t.Fatal("unsafe or incomplete publication settings")
	}
	for i, rule := range document.Statement {
		if rule.Effect != "Allow" || rule.Principal != "*" {
			t.Fatal("unexpected principal or effect")
		}
		if i == 0 {
			if rule.Action != "s3:GetObject" || string(rule.Resource) != `"arn:aws:s3:::test.example/*"` || len(rule.Condition) != 0 {
				t.Fatal("anonymous access must only allow object reads")
			}
			continue
		}
		var resources []string
		if json.Unmarshal(rule.Resource, &resources) != nil || !reflect.DeepEqual(resources, []string{"arn:aws:s3:::test.example", "arn:aws:s3:::test.example/*"}) || rule.Action != "s3:*" || len(rule.Condition) != 1 {
			t.Fatal("management access is not scoped to this bucket")
		}
		if i == 1 && !reflect.DeepEqual(rule.Condition, map[string]map[string]string{"StringEquals": {"yc:access-key-id": "fake-access"}}) {
			t.Fatal("application must keep access through its authenticated key")
		}
		if i == 2 && !reflect.DeepEqual(rule.Condition, map[string]map[string]string{"Bool": {"aws:PrincipalIsAWSService": "true"}}) {
			t.Fatal("console access must require an authenticated cloud service")
		}
	}
}

func TestPublishErrorIdentifiesStepWithoutResponsePayload(t *testing.T) {
	for _, denied := range []string{"policy", "website"} {
		t.Run(denied, func(t *testing.T) {
			store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusNotFound)
					io.WriteString(w, `<Error><Code>NoSuchBucketPolicy</Code></Error>`)
					return
				}
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

func TestPublishPreservesExistingRulesWithoutDuplicates(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"InfrastructureAdministrator","Effect":"Allow","Principal":{"CanonicalUser":"test-admin"},"Action":"s3:*","Resource":"arn:aws:s3:::test.example"}]}`
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("policy") {
			if r.Method == http.MethodGet {
				io.WriteString(w, policy)
				return
			}
			body, _ := io.ReadAll(r.Body)
			policy = string(body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for range 2 {
		if err := store.Publish(context.Background()); err != nil {
			t.Fatal(err)
		}
		var document struct{ Statement []map[string]any }
		if json.Unmarshal([]byte(policy), &document) != nil || len(document.Statement) != 4 {
			t.Fatal("existing rules lost or application rules duplicated")
		}
		if document.Statement[0]["Sid"] != "InfrastructureAdministrator" || document.Statement[0]["Principal"].(map[string]any)["CanonicalUser"] != "test-admin" {
			t.Fatal("infrastructure access changed")
		}
	}
}

func TestUnreadablePolicyIsNotOverwritten(t *testing.T) {
	for _, body := range []string{"broken JSON", `<Error><Code>AccessDenied</Code><Message>fake-secret</Message></Error>`} {
		store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Error("unreadable policy must not be replaced")
			}
			if strings.HasPrefix(body, "<") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusForbidden)
			}
			io.WriteString(w, body)
		})
		err := store.Publish(context.Background())
		if err == nil || strings.Contains(err.Error(), "fake-secret") {
			t.Fatal("unsafe policy handling")
		}
	}
}
