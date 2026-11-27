package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/storage"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
)

type memMediaObject struct {
	data        []byte
	contentType string
	etag        string
}

type memMediaStore struct {
	mu       sync.Mutex
	objects  map[string]memMediaObject
	putCount int
	headErr  error
	putErr   error
	getErr   error
}

func newMemMediaStore() *memMediaStore {
	return &memMediaStore{
		objects: make(map[string]memMediaObject),
	}
}

func (m *memMediaStore) EnsureBucket(context.Context) error { return nil }

func (m *memMediaStore) Put(_ context.Context, key string, body io.Reader, _ int64, contentType string) (storage.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return storage.ObjectMeta{}, m.putErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return storage.ObjectMeta{}, err
	}
	sum := sha256.Sum256(data)
	etag := fmt.Sprintf("%q", hex.EncodeToString(sum[:16]))
	m.objects[key] = memMediaObject{
		data:        data,
		contentType: contentType,
		etag:        etag,
	}
	m.putCount++
	return storage.ObjectMeta{
		ContentType:   contentType,
		ContentLength: int64(len(data)),
		ETag:          etag,
	}, nil
}

func (m *memMediaStore) Get(_ context.Context, key string) (*storage.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	obj, ok := m.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	cp := append([]byte(nil), obj.data...)
	return &storage.Object{
		ObjectMeta: storage.ObjectMeta{
			ContentType:   obj.contentType,
			ContentLength: int64(len(cp)),
			ETag:          obj.etag,
		},
		Body: io.NopCloser(bytes.NewReader(cp)),
	}, nil
}

func (m *memMediaStore) Head(_ context.Context, key string) (storage.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.headErr != nil {
		return storage.ObjectMeta{}, m.headErr
	}
	obj, ok := m.objects[key]
	if !ok {
		return storage.ObjectMeta{}, storage.ErrNotFound
	}
	return storage.ObjectMeta{
		ContentType:   obj.contentType,
		ContentLength: int64(len(obj.data)),
		ETag:          obj.etag,
	}, nil
}

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func makeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// makeMinimalVP8LWebP builds a valid 1x1 lossless WebP image (VP8L bitstream) for DecodeConfig tests.
func makeMinimalVP8LWebP(w, h int) []byte {
	// VP8L header: 0x2f signature + 28 bits (14-bit width-1, 14-bit height-1, alpha=0, version=0).
	bits := uint32(w-1) | (uint32(h-1) << 14)
	vp8l := []byte{
		0x2f,
		byte(bits),
		byte(bits >> 8),
		byte(bits >> 16),
		byte(bits >> 24),
		0x00, // dummy payload byte
	}
	var chunk bytes.Buffer
	chunk.WriteString("VP8L")
	_ = binary.Write(&chunk, binary.LittleEndian, uint32(len(vp8l)))
	chunk.Write(vp8l)
	if len(vp8l)%2 == 1 {
		chunk.WriteByte(0)
	}
	var riff bytes.Buffer
	riff.WriteString("RIFF")
	_ = binary.Write(&riff, binary.LittleEndian, uint32(4+chunk.Len()))
	riff.WriteString("WEBP")
	riff.Write(chunk.Bytes())
	return riff.Bytes()
}

// makeOversizedHeaderPNG creates a tiny valid PNG header claiming w x h dimensions without allocating a huge pixel buffer.
func makeOversizedHeaderPNG(w, h uint32) []byte {
	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, w)
	_ = binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 2, 0, 0, 0}) // 8-bit RGB

	var out bytes.Buffer
	out.Write(pngMagicBytes)
	_ = binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()))
	out.WriteString("IHDR")
	out.Write(ihdr.Bytes())
	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr.Bytes()...))
	_ = binary.Write(&out, binary.BigEndian, crc)
	return out.Bytes()
}

func newMediaTestRouter(t *testing.T, store storage.Store, rl middleware.RateLimiter) (http.Handler, *auth.Manager) {
	t.Helper()
	authMgr, err := auth.NewManager("media-test-jwt-secret-0123456789", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	r := New(Dependencies{
		ServiceName:         "gateway",
		AuthManager:         authMgr,
		RateLimiter:         rl,
		RateLimitIPPerMin:   300,
		RateLimitUserPerMin: 180,
		MediaStore:          store,
		Logger:              logger.New("gateway-test"),
	})
	return r, authMgr
}

func signMediaToken(t *testing.T, mgr *auth.Manager, userID, role, shopID string) string {
	t.Helper()
	tok, _, _, err := mgr.SignAccessToken(userID, role, shopID)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	return tok
}

func TestSellerMediaUploadSuccessDedupAndServe(t *testing.T) {
	store := newMemMediaStore()
	r, mgr := newMediaTestRouter(t, store, nil)

	const shopA = "11111111-2222-4333-8444-555555555555"
	const shopB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	tokenA := signMediaToken(t, mgr, "seller-a", auth.RoleSeller, shopA)
	tokenB := signMediaToken(t, mgr, "seller-b", auth.RoleSeller, shopB)

	cases := []struct {
		name        string
		contentType string
		ext         string
		payload     []byte
		wantW       int
		wantH       int
	}{
		{"jpeg", "image/jpeg", "jpg", makeTestJPEG(t, 12, 8), 12, 8},
		{"png", "image/png", "png", makeTestPNG(t, 16, 10), 16, 10},
		{"webp", "image/webp", "webp", makeMinimalVP8LWebP(20, 14), 20, 14},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(tc.payload))
			req.Header.Set("Authorization", "Bearer "+tokenA)
			req.Header.Set("Content-Type", tc.contentType)
			req.Header.Set("Content-Disposition", `attachment; filename="secret-original-name.png"`)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}

			var resp struct {
				Media mediaUploadDTO `json:"media"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			sum := sha256.Sum256(tc.payload)
			wantURL := fmt.Sprintf("/media/shops/%s/%s.%s", shopA, hex.EncodeToString(sum[:]), tc.ext)
			if resp.Media.URL != wantURL {
				t.Fatalf("url=%q want %q", resp.Media.URL, wantURL)
			}
			if resp.Media.Width != tc.wantW || resp.Media.Height != tc.wantH {
				t.Fatalf("dims=%dx%d want %dx%d", resp.Media.Width, resp.Media.Height, tc.wantW, tc.wantH)
			}
			if resp.Media.Bytes != int64(len(tc.payload)) || resp.Media.ContentType != tc.contentType {
				t.Fatalf("meta mismatch: %+v", resp.Media)
			}

			// Public GET serves exact bytes + cache/security headers + ETag.
			getReq := httptest.NewRequest(http.MethodGet, resp.Media.URL, nil)
			getRec := httptest.NewRecorder()
			r.ServeHTTP(getRec, getReq)
			if getRec.Code != http.StatusOK {
				t.Fatalf("GET %s status=%d body=%s", resp.Media.URL, getRec.Code, getRec.Body.String())
			}
			if !bytes.Equal(getRec.Body.Bytes(), tc.payload) {
				t.Fatalf("GET body mismatch")
			}
			if got := getRec.Header().Get("Content-Type"); got != tc.contentType {
				t.Fatalf("Content-Type=%q want %q", got, tc.contentType)
			}
			if got := getRec.Header().Get("Cache-Control"); got != mediaCacheControl {
				t.Fatalf("Cache-Control=%q want %q", got, mediaCacheControl)
			}
			if got := getRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options=%q want nosniff", got)
			}
			if got := getRec.Header().Get("Content-Security-Policy"); got != mediaCSPHeader {
				t.Fatalf("CSP=%q want %q", got, mediaCSPHeader)
			}
			etag := getRec.Header().Get("ETag")
			if etag == "" {
				t.Fatal("expected ETag header")
			}

			// Conditional GET with matching If-None-Match -> 304 Not Modified.
			inmReq := httptest.NewRequest(http.MethodGet, resp.Media.URL, nil)
			inmReq.Header.Set("If-None-Match", etag)
			inmRec := httptest.NewRecorder()
			r.ServeHTTP(inmRec, inmReq)
			if inmRec.Code != http.StatusNotModified {
				t.Fatalf("If-None-Match status=%d want 304", inmRec.Code)
			}
			if inmRec.Body.Len() != 0 {
				t.Fatalf("304 response must have empty body, got %d bytes", inmRec.Body.Len())
			}
		})
	}

	// Uploading identical PNG again for shopA reuses existing object (no second Put).
	putsBefore := store.putCount
	pngBytes := makeTestPNG(t, 16, 10)
	dupReq := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(pngBytes))
	dupReq.Header.Set("Authorization", "Bearer "+tokenA)
	dupReq.Header.Set("Content-Type", "image/png")
	dupRec := httptest.NewRecorder()
	r.ServeHTTP(dupRec, dupReq)
	if dupRec.Code != http.StatusCreated {
		t.Fatalf("dup upload status=%d", dupRec.Code)
	}
	if store.putCount != putsBefore {
		t.Fatalf("expected dedup to skip Put, putCount=%d -> %d", putsBefore, store.putCount)
	}

	// Seller B uploading identical bytes gets shopB namespace (never shopA).
	bReq := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(pngBytes))
	bReq.Header.Set("Authorization", "Bearer "+tokenB)
	bReq.Header.Set("Content-Type", "image/png")
	bRec := httptest.NewRecorder()
	r.ServeHTTP(bRec, bReq)
	if bRec.Code != http.StatusCreated || !strings.Contains(bRec.Body.String(), "/media/shops/"+shopB+"/") {
		t.Fatalf("shopB upload unexpected: status=%d body=%s", bRec.Code, bRec.Body.String())
	}
}

func TestSellerMediaUploadValidationErrors(t *testing.T) {
	store := newMemMediaStore()
	r, mgr := newMediaTestRouter(t, store, nil)

	const shopID = "11111111-2222-4333-8444-555555555555"
	sellerTok := signMediaToken(t, mgr, "seller-1", auth.RoleSeller, shopID)
	buyerTok := signMediaToken(t, mgr, "buyer-1", auth.RoleBuyer, "")

	// RBAC: unauthenticated -> 401, buyer -> 403.
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(makeTestPNG(t, 4, 4)))
	unauthReq.Header.Set("Content-Type", "image/png")
	unauthRec := httptest.NewRecorder()
	r.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d want 401", unauthRec.Code)
	}

	buyerReq := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(makeTestPNG(t, 4, 4)))
	buyerReq.Header.Set("Authorization", "Bearer "+buyerTok)
	buyerReq.Header.Set("Content-Type", "image/png")
	buyerRec := httptest.NewRecorder()
	r.ServeHTTP(buyerRec, buyerReq)
	if buyerRec.Code != http.StatusForbidden {
		t.Fatalf("buyer status=%d want 403", buyerRec.Code)
	}

	// 413: Content-Length > 5 MiB.
	oversized := make([]byte, mediaMaxUploadBytes+1)
	copy(oversized, []byte{0xFF, 0xD8, 0xFF})
	req413 := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(oversized))
	req413.Header.Set("Authorization", "Bearer "+sellerTok)
	req413.Header.Set("Content-Type", "image/jpeg")
	rec413 := httptest.NewRecorder()
	r.ServeHTTP(rec413, req413)
	if rec413.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized Content-Length status=%d want 413 body=%s", rec413.Code, rec413.Body.String())
	}

	// 413: chunked transfer (Content-Length == -1) exceeding 5 MiB.
	req413Chunked := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", io.NopCloser(bytes.NewReader(oversized)))
	req413Chunked.ContentLength = -1
	req413Chunked.Header.Set("Authorization", "Bearer "+sellerTok)
	req413Chunked.Header.Set("Content-Type", "image/jpeg")
	rec413Chunked := httptest.NewRecorder()
	r.ServeHTTP(rec413Chunked, req413Chunked)
	if rec413Chunked.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunked status=%d want 413 body=%s", rec413Chunked.Code, rec413Chunked.Body.String())
	}

	// 415: disallowed types & spoofed content (including SVG disguised as JPEG).
	cases415 := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"svg_content_type", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"html_content_type", "text/html", []byte(`<html><script>alert(1)</script></html>`)},
		{"gif_content_type", "image/gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")},
		{"svg_disguised_as_jpeg", "image/jpeg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"html_disguised_as_png", "image/png", []byte(`<!DOCTYPE html><body>xss</body>`)},
		{"gif_disguised_as_webp", "image/webp", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")},
		{"png_sent_as_jpeg", "image/jpeg", makeTestPNG(t, 4, 4)},
		{"jpeg_sent_as_png", "image/png", makeTestJPEG(t, 4, 4)},
	}
	for _, tc := range cases415 {
		t.Run("415_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+sellerTok)
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s status=%d want 415 body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	// 400: empty, corrupted images, fake JPEG magic + SVG payload, dimensions > 6000x6000.
	cases400 := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"empty_body", "image/png", nil},
		{"corrupted_jpeg_with_svg_tail", "image/jpeg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("<svg onload=alert(1)>")...)},
		{"corrupted_png", "image/png", append(append([]byte(nil), pngMagicBytes...), []byte("not-an-ihdr-chunk")...)},
		{"corrupted_webp", "image/webp", []byte("RIFF\x10\x00\x00\x00WEBPbadchunk1234")},
		{"dimensions_over_6000_width", "image/png", makeOversizedHeaderPNG(6001, 100)},
		{"dimensions_over_6000_height", "image/png", makeOversizedHeaderPNG(100, 6001)},
	}
	for _, tc := range cases400 {
		t.Run("400_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+sellerTok)
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s status=%d want 400 body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	if store.putCount != 0 {
		t.Fatalf("expected 0 objects stored after invalid uploads, got %d", store.putCount)
	}
}

func TestGetMediaInvalidKeysAndMissingReturn404(t *testing.T) {
	store := newMemMediaStore()
	r, _ := newMediaTestRouter(t, store, nil)

	const validShop = "11111111-2222-4333-8444-555555555555"
	validHash := strings.Repeat("a", 64)

	badPaths := []string{
		"/media/shops/" + validShop + "/" + validHash + ".jpg", // valid pattern, missing in S3 -> 404
		"/media/shops/" + validShop + "/" + validHash + ".svg",
		"/media/shops/" + validShop + "/" + validHash + ".gif",
		"/media/shops/" + validShop + "/" + validHash + ".html",
		"/media/shops/" + validShop + "/" + strings.Repeat("A", 64) + ".jpg",
		"/media/shops/" + validShop + "/short.jpg",
		"/media/shops/not-a-valid-uuid-at-all-000000000000/" + validHash + ".jpg",
		"/media/shops/" + validShop + "/" + validHash + ".jpg/extra",
		"/media/other/" + validShop + "/" + validHash + ".jpg",
		"/media/shops/../secret.jpg",
		"/media/shops/%2e%2e/" + validHash + ".jpg",
		"/media/",
	}

	for _, p := range badPaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("If-None-Match", `"anything"`)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d want 404 body=%s", p, rec.Code, rec.Body.String())
		}
	}
}

type denyUserRateLimiter struct{}

func (denyUserRateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (middleware.RateLimitResult, error) {
	if strings.HasPrefix(key, "rl:user:") {
		return middleware.RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: time.Minute}, nil
	}
	return middleware.RateLimitResult{Allowed: true, Remaining: limit - 1}, nil
}

func TestSellerMediaUploadEnforcesUserRateLimit(t *testing.T) {
	store := newMemMediaStore()
	r, mgr := newMediaTestRouter(t, store, denyUserRateLimiter{})

	tok := signMediaToken(t, mgr, "seller-rl", auth.RoleSeller, "11111111-2222-4333-8444-555555555555")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(makeTestPNG(t, 4, 4)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "image/png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited upload status=%d want 429 body=%s", rec.Code, rec.Body.String())
	}
}

type hangingMediaStore struct {
	hangHead bool
	hangPut  bool
	hangGet  bool
}

func (s *hangingMediaStore) EnsureBucket(context.Context) error { return nil }

func (s *hangingMediaStore) Head(ctx context.Context, _ string) (storage.ObjectMeta, error) {
	if s.hangHead {
		<-ctx.Done()
		return storage.ObjectMeta{}, ctx.Err()
	}
	return storage.ObjectMeta{}, storage.ErrNotFound
}

func (s *hangingMediaStore) Put(ctx context.Context, _ string, _ io.Reader, _ int64, _ string) (storage.ObjectMeta, error) {
	if s.hangPut {
		<-ctx.Done()
		return storage.ObjectMeta{}, ctx.Err()
	}
	return storage.ObjectMeta{}, nil
}

func (s *hangingMediaStore) Get(ctx context.Context, _ string) (*storage.Object, error) {
	if s.hangGet {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, storage.ErrNotFound
}

func TestMediaHandlersReturn503JSONBeforeDeadlineWhenStorageHangs(t *testing.T) {
	const opTimeout = 80 * time.Millisecond
	const maxAllowedElapsed = 2 * time.Second
	const shopID = "11111111-2222-4333-8444-555555555555"
	validMediaPath := "/media/shops/" + shopID + "/" + strings.Repeat("b", 64) + ".png"

	authMgr, err := auth.NewManager("media-timeout-jwt-secret-0123456789", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sellerTok := signMediaToken(t, authMgr, "seller-hang", auth.RoleSeller, shopID)
	pngBytes := makeTestPNG(t, 4, 4)

	buildRouter := func(st storage.Store) http.Handler {
		return New(Dependencies{
			ServiceName:         "gateway",
			AuthManager:         authMgr,
			MediaStore:          st,
			MediaStorageTimeout: opTimeout,
			Logger:              logger.New("gateway-test", logger.WithWriter(io.Discard)),
		})
	}

	assert503JSON := func(t *testing.T, label string, elapsed time.Duration, rec *httptest.ResponseRecorder) {
		t.Helper()
		if elapsed >= maxAllowedElapsed {
			t.Fatalf("%s took too long (%v >= %v)", label, elapsed, maxAllowedElapsed)
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d want 503 body=%s", label, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s Content-Type=%q want application/json", label, ct)
		}
		if !strings.Contains(rec.Body.String(), `"code":"UNAVAILABLE"`) {
			t.Fatalf("%s expected UNAVAILABLE JSON error, got %s", label, rec.Body.String())
		}
	}

	t.Run("upload_head_hangs", func(t *testing.T) {
		r := buildRouter(&hangingMediaStore{hangHead: true})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(pngBytes))
		req.Header.Set("Authorization", "Bearer "+sellerTok)
		req.Header.Set("Content-Type", "image/png")
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		assert503JSON(t, "upload Head hang", time.Since(start), rec)
	})

	t.Run("upload_put_hangs", func(t *testing.T) {
		r := buildRouter(&hangingMediaStore{hangPut: true})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/media", bytes.NewReader(pngBytes))
		req.Header.Set("Authorization", "Bearer "+sellerTok)
		req.Header.Set("Content-Type", "image/png")
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		assert503JSON(t, "upload Put hang", time.Since(start), rec)
	})

	t.Run("get_if_none_match_head_hangs", func(t *testing.T) {
		r := buildRouter(&hangingMediaStore{hangHead: true})
		req := httptest.NewRequest(http.MethodGet, validMediaPath, nil)
		req.Header.Set("If-None-Match", `"etag-1"`)
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		assert503JSON(t, "GET If-None-Match Head hang", time.Since(start), rec)
	})

	t.Run("get_body_hangs", func(t *testing.T) {
		r := buildRouter(&hangingMediaStore{hangGet: true})
		req := httptest.NewRequest(http.MethodGet, validMediaPath, nil)
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		assert503JSON(t, "GET hang", time.Since(start), rec)
	})
}
