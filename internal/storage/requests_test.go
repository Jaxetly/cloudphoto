package storage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
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
	const token = "page+2%2F"
	requests := 0
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/xml")
		query := r.URL.Query()
		if query.Get("encoding-type") != "url" || query.Get("prefix") != "albums/" {
			t.Error("listing must request URL encoding for object keys")
		}
		switch query.Get("continuation-token") {
		case "":
			io.WriteString(w, `<ListBucketResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextContinuationToken>`+token+`</NextContinuationToken><Contents><Key>albums%2Fa%2Fphotos%2F%25%0D+.jpg</Key></Contents></ListBucketResult>`)
		case token:
			io.WriteString(w, `<ListBucketResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated><Contents><Key>albums%2Fb%2F.album</Key></Contents></ListBucketResult>`)
		default:
			t.Error("continuation token was altered")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	keys, err := store.List(context.Background(), "albums/")
	if err != nil || requests != 2 || !reflect.DeepEqual(keys, []string{"albums/a/photos/%\r+.jpg", "albums/b/.album"}) {
		t.Fatalf("unexpected listing: %q, %v", keys, err)
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

func TestDeleteTargetsOnlyExactObjectPathsAndStopsOnFailure(t *testing.T) {
	keys := []string{"albums/61/photos/Море + #.JPG", "albums/61/.album", "album7.html"}
	var deleted []string
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.RawQuery
		if r.Method != http.MethodDelete || (query != "" && query != "x-id=DeleteObject") || !strings.HasPrefix(r.URL.Path, "/test.example/") {
			t.Error("expected DELETE on an exact object URL")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/test.example/")
		deleted = append(deleted, key)
		if key == keys[1] {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>fake-secret</Message></Error>`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := store.Delete(context.Background(), nil); err != nil || len(deleted) != 0 {
		t.Fatal("empty delete made a request")
	}
	if err := store.Delete(context.Background(), keys); err == nil || strings.Contains(err.Error(), "fake-secret") || !reflect.DeepEqual(deleted, keys[:2]) {
		t.Fatal("delete did not stop safely after the first failing object")
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
	if err := store.Publish(context.Background(), nil); err != nil {
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
	for _, denied := range []string{"policy", "acl", "website"} {
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
			err := store.Publish(context.Background(), []string{"albums/test/photos/test.jpg"})
			if err == nil || strings.Contains(err.Error(), "fake-secret") {
				t.Fatal("unsafe error")
			}
			step := "публичное чтение"
			if denied == "website" {
				step = "хостинг"
			}
			if denied == "acl" {
				step = "чтение фотографии"
			}
			if !strings.Contains(err.Error(), step) {
				t.Fatal("missing operation context")
			}
		})
	}
}

func TestPublishOnlyOpensSpecifiedPhotoObjects(t *testing.T) {
	var public []string
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"Version":"2012-10-17","Statement":[]}`)
			return
		}
		if r.URL.Query().Has("acl") {
			if r.URL.Path == "/test.example" || r.URL.Path == "/test.example/" {
				t.Error("bucket ACL must remain private")
			}
			if r.Header.Get("X-Amz-Acl") != "public-read" || r.Header.Get("Content-MD5") != "1B2M2Y8AsgTpgAmY7PhCfg==" {
				t.Error("unsafe object ACL")
			}
			public = append(public, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	photos := []string{"albums/61/photos/A.JPG", "albums/62/photos/Фото #.jpg"}
	if err := store.Publish(context.Background(), photos); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(public, []string{"/test.example/" + photos[0], "/test.example/" + photos[1]}) {
		t.Fatal("publication changed unrelated objects")
	}
}

func TestPageUploadIsPublicButOrdinaryUploadIsPrivate(t *testing.T) {
	var permissions []string
	store := testStore(t, func(w http.ResponseWriter, r *http.Request) {
		permissions = append(permissions, r.Header.Get("X-Amz-Acl"))
		w.WriteHeader(http.StatusOK)
	})
	if err := store.Put(context.Background(), "photo.jpg", strings.NewReader("photo"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPublic(context.Background(), "index.html", strings.NewReader("page"), 4, "text/html"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(permissions, []string{"", "public-read"}) {
		t.Fatal("unexpected publication permissions")
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
		if err := store.Publish(context.Background(), nil); err != nil {
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
		err := store.Publish(context.Background(), nil)
		if err == nil || strings.Contains(err.Error(), "fake-secret") {
			t.Fatal("unsafe policy handling")
		}
	}
}
