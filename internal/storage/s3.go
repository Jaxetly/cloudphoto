package storage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var ErrNotFound = errors.New("Объект не найден")

type Store interface {
	List(context.Context, string) ([]string, error)
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, io.Reader, int64, string) error
	PutPublic(context.Context, string, io.Reader, int64, string) error
	Delete(context.Context, []string) error
	Publish(context.Context, []string) error
}

type S3 struct {
	client    *s3.Client
	bucket    string
	accessKey string
}

func New(bucket, accessKey, secretKey string) *S3 {
	return &S3{
		bucket:    bucket,
		accessKey: accessKey,
		client: s3.New(s3.Options{
			Region:                     "ru-central1",
			BaseEndpoint:               aws.String("https://storage.yandexcloud.net"),
			UsePathStyle:               true,
			Credentials:                credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
			HTTPClient:                 &http.Client{Timeout: 30 * time.Second},
			RetryMaxAttempts:           3,
			RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
			ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		}),
	}
}

// Сырые ошибки SDK могут содержать подписанный запрос: наружу передаём только причину.
func safeError(err error) error {
	if err == nil {
		return nil
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return ErrNotFound
		case "NoSuchBucket":
			return errors.New("Бакет не найден")
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "403":
			return errors.New("Нет доступа к облачному хранилищу")
		case "InvalidDigest", "BadDigest", "MissingContentMD5":
			return errors.New("Ошибка контрольной суммы S3-запроса")
		case "InvalidRequest", "InvalidArgument", "MalformedXML", "MalformedPolicy":
			return errors.New("Некорректный формат S3-запроса")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("Хранилище не ответило вовремя")
	}
	return errors.New("Ошибка обращения к облачному хранилищу")
}

func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var token *string
	seen := map[string]bool{}
	for {
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		page, err := s.client.ListObjectsV2(request, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), ContinuationToken: token,
		})
		cancel()
		if err != nil {
			return nil, safeError(err)
		}
		for _, object := range page.Contents {
			if object.Key != nil {
				keys = append(keys, *object.Key)
			}
		}
		if !aws.ToBool(page.IsTruncated) {
			return keys, nil
		}
		next := aws.ToString(page.NextContinuationToken)
		if next == "" || seen[next] {
			return nil, errors.New("Хранилище вернуло некорректную страницу списка")
		}
		seen[next] = true
		token = aws.String(next)
	}
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	out, err := s.client.GetObject(request, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		cancel()
		return nil, safeError(err)
	}
	return &timedBody{ReadCloser: out.Body, cancel: cancel}, nil
}

type timedBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *timedBody) Close() error { err := body.ReadCloser.Close(); body.cancel(); return err }

func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	return s.put(ctx, key, body, size, contentType, "")
}

func (s *S3) PutPublic(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	return s.put(ctx, key, body, size, contentType, types.ObjectCannedACLPublicRead)
}

func (s *S3) put(ctx context.Context, key string, body io.Reader, size int64, contentType string, acl types.ObjectCannedACL) error {
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err := s.client.PutObject(request, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType), ACL: acl,
	})
	return safeError(err)
}

func (s *S3) Delete(ctx context.Context, keys []string) error {
	for start := 0; start < len(keys); start += 1000 {
		end := min(start+1000, len(keys))
		objects := make([]types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(key)})
		}
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		result, err := s.client.DeleteObjects(request, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		}, withContentMD5)
		cancel()
		if err != nil {
			return safeError(err)
		}
		if len(result.Errors) != 0 {
			return errors.New("Не все объекты удалось удалить")
		}
	}
	return nil
}

func (s *S3) Publish(ctx context.Context, photos []string) error {
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	existing, err := s.client.GetBucketPolicy(request, &s3.GetBucketPolicyInput{Bucket: aws.String(s.bucket)})
	cancel()
	var statements []json.RawMessage
	if err != nil {
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != "NoSuchBucketPolicy" {
			return fmt.Errorf("Не удалось прочитать политику доступа: %w", safeError(err))
		}
	} else {
		var document struct{ Statement []json.RawMessage }
		if json.Unmarshal([]byte(aws.ToString(existing.Policy)), &document) != nil {
			return errors.New("Не удалось прочитать политику доступа")
		}
		for _, statement := range document.Statement {
			var rule struct{ Sid string }
			if json.Unmarshal(statement, &rule) != nil {
				return errors.New("Не удалось прочитать политику доступа")
			}
			if rule.Sid != "CloudphotoPublicPhotos" && rule.Sid != "CloudphotoApplication" && rule.Sid != "CloudphotoConsole" {
				statements = append(statements, statement)
			}
		}
	}
	// Политика Яндекса — белый список: одного публичного GetObject недостаточно,
	// иначе приложение потеряет доступ сразу после PutBucketPolicy.
	resource := "arn:aws:s3:::" + s.bucket
	for _, rule := range []map[string]any{
		{"Sid": "CloudphotoPublicPhotos", "Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": resource + "/*"},
		{"Sid": "CloudphotoApplication", "Effect": "Allow", "Principal": "*", "Action": "s3:*", "Resource": []string{resource, resource + "/*"},
			"Condition": map[string]any{"StringEquals": map[string]string{"yc:access-key-id": s.accessKey}}},
		{"Sid": "CloudphotoConsole", "Effect": "Allow", "Principal": "*", "Action": "s3:*", "Resource": []string{resource, resource + "/*"},
			"Condition": map[string]any{"Bool": map[string]string{"aws:PrincipalIsAWSService": "true"}}},
	} {
		encoded, marshalErr := json.Marshal(rule)
		if marshalErr != nil {
			return errors.New("Не удалось подготовить политику доступа")
		}
		statements = append(statements, encoded)
	}
	policy, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": statements})
	if err != nil {
		return errors.New("Не удалось подготовить политику доступа")
	}
	request, cancel = context.WithTimeout(ctx, 45*time.Second)
	_, err = s.client.PutBucketPolicy(request, &s3.PutBucketPolicyInput{Bucket: aws.String(s.bucket), Policy: aws.String(string(policy))}, withContentMD5)
	cancel()
	if err != nil {
		return fmt.Errorf("Не удалось настроить публичное чтение: %w", safeError(err))
	}
	// Публичным становится только содержимое сайта, а не ACL всего бакета.
	for _, key := range photos {
		request, cancel = context.WithTimeout(ctx, 45*time.Second)
		_, err = s.client.PutObjectAcl(request, &s3.PutObjectAclInput{
			Bucket: aws.String(s.bucket), Key: aws.String(key), ACL: types.ObjectCannedACLPublicRead,
		}, withContentMD5)
		cancel()
		if err != nil {
			return fmt.Errorf("Не удалось открыть чтение фотографии: %w", safeError(err))
		}
	}
	request, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err = s.client.PutBucketWebsite(request, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(s.bucket), WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{Suffix: aws.String("index.html")},
			ErrorDocument: &types.ErrorDocument{Key: aws.String("error.html")},
		},
	}, withContentMD5)
	if err != nil {
		return fmt.Errorf("Не удалось настроить хостинг: %w", safeError(err))
	}
	return nil
}

// S3 API Яндекса требует Content-MD5 для XML пакетного удаления.
// MD5 здесь служит контрольной суммой запроса, а не защитой учётных данных.
func withContentMD5(options *s3.Options) {
	options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("ContentMD5", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
			request, ok := in.Request.(*smithyhttp.Request)
			if !ok {
				return middleware.BuildOutput{}, middleware.Metadata{}, errors.New("Некорректный S3-запрос")
			}
			hash := md5.New()
			if stream := request.GetStream(); stream != nil {
				if _, err := io.Copy(hash, stream); err != nil {
					return middleware.BuildOutput{}, middleware.Metadata{}, err
				}
			}
			if err := request.RewindStream(); err != nil {
				return middleware.BuildOutput{}, middleware.Metadata{}, err
			}
			request.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(hash.Sum(nil)))
			return next.HandleBuild(ctx, in)
		}), middleware.After)
	})
}

func IsPhoto(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg")
}
