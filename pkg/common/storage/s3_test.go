package storage

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // ETag simulation in unit test fake S3 server.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeS3Object struct {
	data         []byte
	contentType  string
	etag         string
	lastModified time.Time
}

type fakeS3Server struct {
	mu           sync.Mutex
	buckets      map[string]bool
	objects      map[string]map[string]fakeS3Object
	createRacing bool
	lastAuthHdr  string
	lastPath     string
}

func newFakeS3Server() *fakeS3Server {
	return &fakeS3Server{
		buckets: make(map[string]bool),
		objects: make(map[string]map[string]fakeS3Object),
	}
}

func (f *fakeS3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastAuthHdr = r.Header.Get("Authorization")
	f.lastPath = r.URL.Path

	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	if trimmed == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(trimmed, "/", 2)
	bucket := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}

	// Bucket-level operations (path-style: /<bucket>).
	if key == "" {
		switch r.Method {
		case http.MethodHead:
			if !f.buckets[bucket] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			if f.createRacing {
				f.buckets[bucket] = true
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `<Error><Code>BucketAlreadyOwnedByYou</Code><Message>bucket exists</Message></Error>`)
				return
			}
			f.buckets[bucket] = true
			if f.objects[bucket] == nil {
				f.objects[bucket] = make(map[string]fakeS3Object)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	if !f.buckets[bucket] {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code><Message>bucket missing</Message></Error>`)
		return
	}

	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		sum := md5.Sum(body) //nolint:gosec // Standard S3 ETag format in test fake.
		etag := fmt.Sprintf("%q", hex.EncodeToString(sum[:]))
		if f.objects[bucket] == nil {
			f.objects[bucket] = make(map[string]fakeS3Object)
		}
		f.objects[bucket][key] = fakeS3Object{
			data:         body,
			contentType:  r.Header.Get("Content-Type"),
			etag:         etag,
			lastModified: time.Now().UTC().Truncate(time.Second),
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		obj, ok := f.objects[bucket][key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", obj.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("ETag", obj.etag)
		w.Header().Set("Last-Modified", obj.lastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		obj, ok := f.objects[bucket][key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>key missing</Message></Error>`)
			return
		}
		w.Header().Set("Content-Type", obj.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("ETag", obj.etag)
		w.Header().Set("Last-Modified", obj.lastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.data)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestNewValidation(t *testing.T) {
	valid := Config{
		Endpoint:  "http://seaweedfs:8333",
		Bucket:    "nexusmart-media-public",
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
	}
	if _, err := New(valid); err != nil {
		t.Fatalf("expected valid config to succeed, got %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"missing endpoint", func(c *Config) { c.Endpoint = "" }},
		{"bad endpoint scheme", func(c *Config) { c.Endpoint = "ftp://seaweedfs:8333" }},
		{"missing bucket", func(c *Config) { c.Bucket = "" }},
		{"missing access key", func(c *Config) { c.AccessKey = "" }},
		{"missing secret key", func(c *Config) { c.SecretKey = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mut(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://seaweedfs:8333")
	t.Setenv("S3_BUCKET", "custom-bucket")
	t.Setenv("S3_REGION", "ap-southeast-1")
	t.Setenv("S3_ACCESS_KEY", "test-ak")
	t.Setenv("S3_SECRET_KEY", "test-sk")

	cfg := LoadConfigFromEnv()
	if !cfg.Enabled() {
		t.Fatal("expected Enabled() to be true")
	}
	if cfg.Endpoint != "http://seaweedfs:8333" || cfg.Bucket != "custom-bucket" ||
		cfg.Region != "ap-southeast-1" || cfg.AccessKey != "test-ak" || cfg.SecretKey != "test-sk" {
		t.Fatalf("unexpected env config: %+v", cfg)
	}
}

func TestEnsureBucketPutHeadGetLifecycle(t *testing.T) {
	fake := newFakeS3Server()
	ts := httptest.NewServer(fake)
	defer ts.Close()

	client, err := New(Config{
		Endpoint:   ts.URL,
		Bucket:     "nexusmart-media-public",
		Region:     "us-east-1",
		AccessKey:  "dev-ak",
		SecretKey:  "dev-sk",
		HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()

	// EnsureBucket creates bucket when missing and is idempotent when present.
	if err := client.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket (create): %v", err)
	}
	if err := client.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket (idempotent): %v", err)
	}

	// Missing object returns ErrNotFound for both Head and Get.
	if _, err := client.Head(ctx, "shops/missing.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Head missing = %v, want ErrNotFound", err)
	}
	if _, err := client.Get(ctx, "shops/missing.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}

	// Put seekable payload (standard SigV4 signed upload) using path-style URL.
	key := "shops/11111111-2222-3333-4444-555555555555/abcdef.png"
	payload := []byte("png-binary-bytes-12345")
	putMeta, err := client.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if putMeta.ETag == "" || putMeta.ContentLength != int64(len(payload)) || putMeta.ContentType != "image/png" {
		t.Fatalf("unexpected Put meta: %+v", putMeta)
	}

	fake.mu.Lock()
	gotPath := fake.lastPath
	gotAuth := fake.lastAuthHdr
	fake.mu.Unlock()
	if gotPath != "/nexusmart-media-public/"+key {
		t.Fatalf("expected path-style path /nexusmart-media-public/%s, got %s", key, gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("expected AWS SigV4 Authorization header, got %q", gotAuth)
	}

	// Head returns matching metadata.
	headMeta, err := client.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if headMeta.ContentType != "image/png" || headMeta.ContentLength != int64(len(payload)) || headMeta.ETag != putMeta.ETag {
		t.Fatalf("unexpected Head meta: %+v", headMeta)
	}

	// Get streams the exact bytes and metadata.
	obj, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = obj.Body.Close() }()
	gotBytes, err := io.ReadAll(obj.Body)
	if err != nil {
		t.Fatalf("read Get body: %v", err)
	}
	if !bytes.Equal(gotBytes, payload) {
		t.Fatalf("Get body mismatch: got %q, want %q", gotBytes, payload)
	}
	if obj.ContentType != "image/png" || obj.ETag != putMeta.ETag {
		t.Fatalf("unexpected Get meta: %+v", obj.ObjectMeta)
	}

	// Put also supports non-seekable io.Reader streams.
	streamKey := "shops/11111111-2222-3333-4444-555555555555/stream.webp"
	streamPayload := []byte("webp-stream-payload")
	if _, err := client.Put(ctx, streamKey, io.NopCloser(bytes.NewReader(streamPayload)), int64(len(streamPayload)), "image/webp"); err != nil {
		t.Fatalf("Put non-seekable reader: %v", err)
	}
}

func TestEnsureBucketHandlesAlreadyOwnedRace(t *testing.T) {
	fake := newFakeS3Server()
	fake.createRacing = true
	ts := httptest.NewServer(fake)
	defer ts.Close()

	client, err := New(Config{
		Endpoint:   ts.URL,
		Bucket:     "race-bucket",
		AccessKey:  "dev-ak",
		SecretKey:  "dev-sk",
		HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := client.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket should ignore BucketAlreadyOwnedByYou race, got %v", err)
	}
}

func TestClientObeysContextDeadlineOnHungS3Server(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		ts.Close()
	}()

	client, err := New(Config{
		Endpoint:   ts.URL,
		Bucket:     "hung-bucket",
		AccessKey:  "dev-ak",
		SecretKey:  "dev-sk",
		HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const deadline = 150 * time.Millisecond
	const maxElapsed = 2 * time.Second

	t.Run("Head", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		start := time.Now()
		_, err := client.Head(ctx, "shops/test/obj.png")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Head hung: want DeadlineExceeded, got %v", err)
		}
		if elapsed := time.Since(start); elapsed >= maxElapsed {
			t.Fatalf("Head hung too long: %v", elapsed)
		}
	})

	t.Run("Put", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		start := time.Now()
		payload := []byte("img")
		_, err := client.Put(ctx, "shops/test/obj.png", bytes.NewReader(payload), int64(len(payload)), "image/png")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Put hung: want DeadlineExceeded, got %v", err)
		}
		if elapsed := time.Since(start); elapsed >= maxElapsed {
			t.Fatalf("Put hung too long: %v", elapsed)
		}
	})

	t.Run("Get", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		start := time.Now()
		_, err := client.Get(ctx, "shops/test/obj.png")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Get hung: want DeadlineExceeded, got %v", err)
		}
		if elapsed := time.Since(start); elapsed >= maxElapsed {
			t.Fatalf("Get hung too long: %v", elapsed)
		}
	})
}
