package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/storage"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"golang.org/x/image/webp"
)

const (
	mediaMaxUploadBytes        int64 = 5 << 20 // 5 MiB
	mediaMaxDimension                = 6000
	mediaCacheControl                = "public, max-age=31536000, immutable"
	mediaCSPHeader                   = "default-src 'none'"
	defaultMediaStorageTimeout       = 15 * time.Second
)

func (h *handler) mediaOpTimeout() time.Duration {
	if h != nil && h.mediaStorageTimeout > 0 {
		return h.mediaStorageTimeout
	}
	return defaultMediaStorageTimeout
}

var (
	mediaKeyPattern    = regexp.MustCompile(`^shops/[0-9a-f-]{36}/[0-9a-f]{64}\.(jpg|png|webp)$`)
	mediaShopIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	pngMagicBytes      = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
)

type mediaUploadDTO struct {
	URL         string `json:"url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
}

func (h *handler) handleSellerUploadMedia(w http.ResponseWriter, req *http.Request) {
	if h.mediaStore == nil {
		httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage not configured")
		return
	}

	claims, ok := auth.ClaimsFromContext(req.Context())
	if !ok {
		httpx.WriteError(w, req, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	shopID := strings.ToLower(strings.TrimSpace(claims.ShopID))
	if !mediaShopIDPattern.MatchString(shopID) {
		httpx.WriteError(w, req, http.StatusForbidden, "PERMISSION_DENIED", "seller shop context is invalid")
		return
	}
	if _, err := uuid.Parse(shopID); err != nil {
		httpx.WriteError(w, req, http.StatusForbidden, "PERMISSION_DENIED", "seller shop context is invalid")
		return
	}

	if req.ContentLength > mediaMaxUploadBytes {
		httpx.WriteError(w, req, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image exceeds 5 MB limit")
		return
	}

	declaredMIME, ok := parseAllowedImageContentType(req.Header.Get("Content-Type"))
	if !ok {
		httpx.WriteError(w, req, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "only image/jpeg, image/png, and image/webp are supported")
		return
	}

	if req.Body == nil {
		httpx.WriteError(w, req, http.StatusBadRequest, "INVALID_ARGUMENT", "image body is required")
		return
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, mediaMaxUploadBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpx.WriteError(w, req, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image exceeds 5 MB limit")
			return
		}
		httpx.WriteError(w, req, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read image body")
		return
	}
	if int64(len(data)) > mediaMaxUploadBytes {
		httpx.WriteError(w, req, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image exceeds 5 MB limit")
		return
	}
	if len(data) == 0 {
		httpx.WriteError(w, req, http.StatusBadRequest, "INVALID_ARGUMENT", "image body is empty")
		return
	}

	detectedMIME := detectAllowedImageMIME(data)
	if detectedMIME == "" || detectedMIME != declaredMIME {
		httpx.WriteError(w, req, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "file content does not match allowed image type")
		return
	}

	imgCfg, err := decodeAllowedImageConfig(detectedMIME, data)
	if err != nil || imgCfg.Width <= 0 || imgCfg.Height <= 0 {
		httpx.WriteError(w, req, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid or corrupted image")
		return
	}
	if imgCfg.Width > mediaMaxDimension || imgCfg.Height > mediaMaxDimension {
		httpx.WriteError(w, req, http.StatusBadRequest, "INVALID_ARGUMENT", "image dimensions exceed 6000x6000 limit")
		return
	}

	// ponytail: EXIF/metadata stripping is deferred (ceiling: uploaded JPEG/WebP images retain camera/GPS EXIF tags until server-side re-encode is added).
	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("shops/%s/%s.%s", shopID, hashHex, extForImageMIME(detectedMIME))

	storeCtx, cancel := context.WithTimeout(req.Context(), h.mediaOpTimeout())
	defer cancel()

	_, headErr := h.mediaStore.Head(storeCtx, key)
	if headErr != nil {
		if !errors.Is(headErr, storage.ErrNotFound) {
			h.logMediaError(req, "head media object failed", key, headErr)
			httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage temporarily unavailable")
			return
		}
		if _, putErr := h.mediaStore.Put(storeCtx, key, bytes.NewReader(data), int64(len(data)), detectedMIME); putErr != nil {
			h.logMediaError(req, "put media object failed", key, putErr)
			httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage temporarily unavailable")
			return
		}
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"media": mediaUploadDTO{
			URL:         "/media/" + key,
			Width:       imgCfg.Width,
			Height:      imgCfg.Height,
			Bytes:       int64(len(data)),
			ContentType: detectedMIME,
		},
	})
}

func (h *handler) handleGetMedia(w http.ResponseWriter, req *http.Request) {
	key, ok := extractStrictMediaKey(req)
	if !ok {
		httpx.WriteError(w, req, http.StatusNotFound, "NOT_FOUND", "media not found")
		return
	}
	if h.mediaStore == nil {
		httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage not configured")
		return
	}

	fallbackETag := mediaFallbackETag(key)

	storeCtx, cancel := context.WithTimeout(req.Context(), h.mediaOpTimeout())
	defer cancel()

	if inm := strings.TrimSpace(req.Header.Get("If-None-Match")); inm != "" {
		meta, err := h.mediaStore.Head(storeCtx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				httpx.WriteError(w, req, http.StatusNotFound, "NOT_FOUND", "media not found")
				return
			}
			h.logMediaError(req, "head media object failed", key, err)
			httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage temporarily unavailable")
			return
		}
		etag := normalizeETag(meta.ETag, fallbackETag)
		if etagMatchesIfNoneMatch(inm, etag) {
			setMediaHeaders(w, safeServedImageContentType(meta.ContentType, key), etag, 0)
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	obj, err := h.mediaStore.Get(storeCtx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			httpx.WriteError(w, req, http.StatusNotFound, "NOT_FOUND", "media not found")
			return
		}
		h.logMediaError(req, "get media object failed", key, err)
		httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "media storage temporarily unavailable")
		return
	}
	defer func() { _ = obj.Body.Close() }()

	etag := normalizeETag(obj.ETag, fallbackETag)
	setMediaHeaders(w, safeServedImageContentType(obj.ContentType, key), etag, obj.ContentLength)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, obj.Body)
}

func (h *handler) logMediaError(req *http.Request, msg, key string, err error) {
	log := h.log
	if log == nil {
		log = slog.Default()
	}
	log.ErrorContext(req.Context(), msg,
		slog.String("media_key", key),
		slog.String("error", err.Error()),
	)
}

func extractStrictMediaKey(req *http.Request) (string, bool) {
	if req == nil || req.URL == nil {
		return "", false
	}
	if !strings.HasPrefix(req.URL.Path, "/media/") {
		return "", false
	}
	key := strings.TrimPrefix(req.URL.Path, "/media/")
	// Reject any URL-encoded path tricks (%2e, %2f, %5c, etc.) where RawPath diverges from Path.
	if req.URL.RawPath != "" && strings.TrimPrefix(req.URL.RawPath, "/media/") != key {
		return "", false
	}
	if !mediaKeyPattern.MatchString(key) {
		return "", false
	}
	// shops/<36-char-uuid>/<64-hex>.<ext>
	shopSeg := key[len("shops/") : len("shops/")+36]
	if _, err := uuid.Parse(shopSeg); err != nil {
		return "", false
	}
	return key, true
}

func parseAllowedImageContentType(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(mt)) {
	case "image/jpeg":
		return "image/jpeg", true
	case "image/png":
		return "image/png", true
	case "image/webp":
		return "image/webp", true
	default:
		return "", false
	}
}

func detectAllowedImageMIME(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && bytes.Equal(data[:8], pngMagicBytes):
		return "image/png"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func decodeAllowedImageConfig(mimeType string, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch mimeType {
	case "image/jpeg":
		return jpeg.DecodeConfig(r)
	case "image/png":
		return png.DecodeConfig(r)
	case "image/webp":
		return webp.DecodeConfig(r)
	default:
		return image.Config{}, errors.New("unsupported image format")
	}
}

func extForImageMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	default:
		return ""
	}
}

func safeServedImageContentType(stored, key string) string {
	if mt, ok := parseAllowedImageContentType(stored); ok {
		return mt
	}
	switch {
	case strings.HasSuffix(key, ".jpg"):
		return "image/jpeg"
	case strings.HasSuffix(key, ".png"):
		return "image/png"
	case strings.HasSuffix(key, ".webp"):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func mediaFallbackETag(key string) string {
	slash := strings.LastIndex(key, "/")
	dot := strings.LastIndex(key, ".")
	if slash >= 0 && dot > slash+1 {
		return fmt.Sprintf("%q", key[slash+1:dot])
	}
	return ""
}

func normalizeETag(etag, fallback string) string {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return fallback
	}
	return etag
}

func etagMatchesIfNoneMatch(headerVal, etag string) bool {
	if etag == "" {
		return false
	}
	if strings.TrimSpace(headerVal) == "*" {
		return true
	}
	cleanTarget := strings.TrimPrefix(strings.TrimSpace(etag), "W/")
	for _, part := range strings.Split(headerVal, ",") {
		cand := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if cand == cleanTarget {
			return true
		}
	}
	return false
}

func setMediaHeaders(w http.ResponseWriter, contentType, etag string, contentLength int64) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", mediaCacheControl)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaCSPHeader)
	if etag != "" {
		h.Set("ETag", etag)
	}
	if contentLength > 0 {
		h.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
}
