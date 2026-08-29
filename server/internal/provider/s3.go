package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	appconfig "tokendance/internal/config"
)

type S3ObjectStorage struct {
	bucket    string
	client    *s3.Client
	presigner *s3.PresignClient
}

func NewObjectStorage(cfg *appconfig.Config) (ObjectStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("object storage config is required")
	}
	switch cfg.ObjectProvider {
	case "memory":
		if cfg.Environment != "development" && cfg.Environment != "test" {
			return nil, fmt.Errorf("memory object storage is only allowed in explicit development or test environments")
		}
		return NewMemoryObjectStorage(""), nil
	case "s3":
		return NewS3ObjectStorage(context.Background(), S3Options{
			Endpoint:     cfg.ObjectEndpoint,
			Region:       cfg.ObjectRegion,
			Bucket:       cfg.ObjectBucket,
			AccessKey:    cfg.ObjectAccessKey,
			SecretKey:    cfg.ObjectSecretKey,
			SessionToken: cfg.ObjectSessionToken,
			UsePathStyle: cfg.ObjectUsePathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported object provider %q", cfg.ObjectProvider)
	}
}

type S3Options struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	SessionToken string
	UsePathStyle bool
	HTTPClient   aws.HTTPClient
}

func NewS3ObjectStorage(ctx context.Context, opts S3Options) (*S3ObjectStorage, error) {
	if strings.TrimSpace(opts.Endpoint) == "" || strings.TrimSpace(opts.Region) == "" || strings.TrimSpace(opts.Bucket) == "" {
		return nil, fmt.Errorf("S3 endpoint, region, and bucket are required")
	}
	if opts.AccessKey == "" || opts.SecretKey == "" {
		return nil, fmt.Errorf("S3 credentials are required")
	}
	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(opts.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, opts.SessionToken)),
	}
	if opts.HTTPClient != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(opts.HTTPClient))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(opts.Endpoint, "/"))
		o.UsePathStyle = opts.UsePathStyle
	})
	return &S3ObjectStorage{
		bucket:    opts.Bucket,
		client:    client,
		presigner: s3.NewPresignClient(client),
	}, nil
}

func (s *S3ObjectStorage) PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	input := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: data}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	_, err := s.client.PutObject(ctx, input)
	return mapS3Error("put", key, err)
}

func (s *S3ObjectStorage) HeadObject(ctx context.Context, key string) (*ObjectMeta, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, mapS3Error("head", key, err)
	}
	meta := &ObjectMeta{Key: key, Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), ETag: strings.Trim(aws.ToString(out.ETag), "\"")}
	if out.LastModified != nil {
		meta.LastModified = *out.LastModified
	}
	return meta, nil
}

func (s *S3ObjectStorage) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, mapS3Error("open", key, err)
	}
	return out.Body, nil
}

func (s *S3ObjectStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.OpenObject(ctx, key)
}

func (s *S3ObjectStorage) PresignDownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	out, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", mapS3Error("presign download", key, err)
	}
	return out.URL, nil
}

func (s *S3ObjectStorage) PresignUploadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	out, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", mapS3Error("presign upload", key, err)
	}
	return out.URL, nil
}

func (s *S3ObjectStorage) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return mapS3Error("delete", key, err)
}

func mapS3Error(operation, key string, err error) error {
	if err == nil {
		return nil
	}
	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) {
		if responseErr.HTTPStatusCode() == http.StatusNotFound {
			return ErrObjectNotFound
		}
		if responseErr.HTTPStatusCode() == http.StatusTooManyRequests || responseErr.HTTPStatusCode() >= 500 {
			return &ProviderError{Code: "S3_UNAVAILABLE", Message: fmt.Sprintf("S3 %s failed for object %q", operation, key), Transient: true, Err: err}
		}
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
		return ErrObjectNotFound
	}
	return &ProviderError{Code: "S3_REQUEST_FAILED", Message: fmt.Sprintf("S3 %s failed for object %q", operation, key), Err: err}
}
