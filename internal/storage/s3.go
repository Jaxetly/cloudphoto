package storage

import (
	"context"
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
)

var ErrNotFound = errors.New("Объект не найден")

type Store interface {
	List(context.Context, string) ([]string, error)
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, io.Reader, int64, string) error
	Delete(context.Context, []string) error
	Publish(context.Context) error
}

type S3 struct {
	client *s3.Client
	bucket string
}

func New(bucket, accessKey, secretKey string) *S3 {
	return &S3{
		bucket: bucket,
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
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err := s.client.PutObject(request, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
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
		})
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

func (s *S3) Publish(ctx context.Context) error {
	// Имя бакета ограничено синтаксисом S3, но JSON всё равно строится безопасно.
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":%q}]}`, "arn:aws:s3:::"+s.bucket+"/*")
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	_, err := s.client.PutBucketPolicy(request, &s3.PutBucketPolicyInput{Bucket: aws.String(s.bucket), Policy: aws.String(policy)})
	cancel()
	if err != nil {
		return safeError(err)
	}
	request, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err = s.client.PutBucketWebsite(request, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(s.bucket), WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{Suffix: aws.String("index.html")},
			ErrorDocument: &types.ErrorDocument{Key: aws.String("error.html")},
		},
	})
	return safeError(err)
}

func IsPhoto(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg")
}
