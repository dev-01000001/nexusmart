package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/nexusmart/core/pkg/common/env"
)

// ErrNotFound is returned by Get and Head when the requested object does not exist.
var ErrNotFound = errors.New("storage: object not found")

const (
	defaultBucket = "nexusmart-media-public"
	defaultRegion = "us-east-1"
)

// Config holds S3-compatible object storage connection parameters.
// Path-style addressing is enabled by default for SeaweedFS/self-hosted S3 compatibility.
type Config struct {
	Endpoint         string
	Bucket           string
	Region           string
	AccessKey        string
	SecretKey        string
	DisablePathStyle bool
	HTTPClient       *http.Client
}

// LoadConfigFromEnv loads S3 object storage configuration from S3_* environment variables.
func LoadConfigFromEnv() Config {
	return Config{
		Endpoint:  env.EnvString("S3_ENDPOINT", ""),
		Bucket:    env.EnvString("S3_BUCKET", defaultBucket),
		Region:    env.EnvString("S3_REGION", defaultRegion),
		AccessKey: env.EnvString("S3_ACCESS_KEY", ""),
		SecretKey: env.EnvString("S3_SECRET_KEY", ""),
	}
}

// Enabled reports whether an S3 endpoint is configured.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.Endpoint) != ""
}

// ObjectMeta describes stored object headers returned by Put, Get, and Head.
type ObjectMeta struct {
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  time.Time
}

// Object holds a streamed object body and its metadata. Callers must close Body.
type Object struct {
	ObjectMeta
	Body io.ReadCloser
}

// Store defines the minimal S3 object operations used by NexusMart services.
type Store interface {
	EnsureBucket(ctx context.Context) error
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (ObjectMeta, error)
	Get(ctx context.Context, key string) (*Object, error)
	Head(ctx context.Context, key string) (ObjectMeta, error)
}

// Client is a compact S3-compatible Store backed by aws-sdk-go-v2/service/s3.
type Client struct {
	s3     *s3.Client
	bucket string
}

var _ Store = (*Client)(nil)

// New constructs a validated path-style S3 client with bounded timeouts and retries.
func New(cfg Config) (*Client, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("storage: S3_ENDPOINT is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("storage: invalid S3_ENDPOINT %q", endpoint)
	}

	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("storage: S3_BUCKET is required")
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultRegion
	}

	accessKey := strings.TrimSpace(cfg.AccessKey)
	secretKey := strings.TrimSpace(cfg.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("storage: S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}

	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       region,
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		),
		UsePathStyle: !cfg.DisablePathStyle,
		HTTPClient:   httpClient,
		Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 3
			o.MaxBackoff = 2 * time.Second
		}),
		// Avoid aws-chunked trailing checksum headers that some S3-compatible servers reject.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})

	return &Client{
		s3:     s3Client,
		bucket: bucket,
	}, nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
}

// Bucket returns the configured target bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}

// EnsureBucket verifies the configured bucket exists, creating it idempotently if missing.
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err == nil {
		return nil
	}
	if !isNotFoundError(err) {
		return fmt.Errorf("storage: head bucket %q: %w", c.bucket, err)
	}

	_, err = c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil && !isBucketAlreadyExistsError(err) {
		return fmt.Errorf("storage: create bucket %q: %w", c.bucket, err)
	}
	return nil
}

// Put uploads an object under key with the given byte size and content type.
func (c *Client) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (ObjectMeta, error) {
	if strings.TrimSpace(key) == "" {
		return ObjectMeta{}, errors.New("storage: key is required")
	}
	if body == nil {
		return ObjectMeta{}, errors.New("storage: body is required")
	}
	if size < 0 {
		return ObjectMeta{}, errors.New("storage: size must be >= 0")
	}

	in := &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}
	if ct := strings.TrimSpace(contentType); ct != "" {
		in.ContentType = aws.String(ct)
	}

	var opts []func(*s3.Options)
	if _, seekable := body.(io.ReadSeeker); !seekable {
		opts = append(opts, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	}

	out, err := c.s3.PutObject(ctx, in, opts...)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("storage: put object %q: %w", key, err)
	}
	return ObjectMeta{
		ContentType:   strings.TrimSpace(contentType),
		ContentLength: size,
		ETag:          aws.ToString(out.ETag),
	}, nil
}

// Get opens a streaming read of the object at key. Returns ErrNotFound if missing.
func (c *Client) Get(ctx context.Context, key string) (*Object, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("storage: key is required")
	}

	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundError(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: get object %q: %w", key, err)
	}

	meta := ObjectMeta{
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          aws.ToString(out.ETag),
	}
	if out.LastModified != nil {
		meta.LastModified = out.LastModified.UTC()
	}
	return &Object{
		ObjectMeta: meta,
		Body:       out.Body,
	}, nil
}

// Head fetches object metadata without downloading the object body. Returns ErrNotFound if missing.
func (c *Client) Head(ctx context.Context, key string) (ObjectMeta, error) {
	if strings.TrimSpace(key) == "" {
		return ObjectMeta{}, errors.New("storage: key is required")
	}

	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundError(err) {
			return ObjectMeta{}, ErrNotFound
		}
		return ObjectMeta{}, fmt.Errorf("storage: head object %q: %w", key, err)
	}

	meta := ObjectMeta{
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          aws.ToString(out.ETag),
	}
	if out.LastModified != nil {
		meta.LastModified = out.LastModified.UTC()
	}
	return meta, nil
}

func isNotFoundError(err error) bool {
	var noSuchKey *types.NoSuchKey
	var notFound *types.NotFound
	var noSuchBucket *types.NoSuchBucket
	if errors.As(err, &noSuchKey) || errors.As(err, &notFound) || errors.As(err, &noSuchBucket) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "NoSuchBucket", "404":
			return true
		}
	}
	var respErr *smithyhttp.ResponseError
	return errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound
}

func isBucketAlreadyExistsError(err error) bool {
	var exists *types.BucketAlreadyExists
	var owned *types.BucketAlreadyOwnedByYou
	if errors.As(err, &exists) || errors.As(err, &owned) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyExists", "BucketAlreadyOwnedByYou":
			return true
		}
	}
	var respErr *smithyhttp.ResponseError
	return errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusConflict
}
