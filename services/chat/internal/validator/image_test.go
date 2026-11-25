package validator

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"testing"
)

func makeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestImageValidatorSSRFAndExtensionBlocking(t *testing.T) {
	ctx := context.Background()
	v := NewImageValidator(ImageValidatorConfig{Mode: ModeMetadata})
	goodMeta := map[string]any{"content_type": "image/png", "size_bytes": 1024}

	blockedURLs := []string{
		"ftp://cdn.example.com/a.png",
		"javascript:alert(1)",
		"https://user:pass@cdn.example.com/a.png",
		"http://127.0.0.1/a.png",
		"http://[::1]/a.png",
		"http://[::ffff:127.0.0.1]/a.png",
		"http://10.0.0.1/a.png",
		"http://172.16.0.5/a.png",
		"http://192.168.1.1/a.png",
		"http://169.254.169.254/latest/meta-data/a.png",
		"http://localhost:8080/a.png",
		"http://metadata.google.internal/a.png",
		"https://cdn.example.com/xss.svg",
		"https://cdn.example.com/page.html",
		"https://cdn.example.com/doc.pdf",
	}
	for _, raw := range blockedURLs {
		if _, err := v.Validate(ctx, raw, goodMeta); !errors.Is(err, ErrInvalidImage) {
			t.Errorf("expected ErrInvalidImage for %q, got %v", raw, err)
		}
	}

	// Valid public URL + valid metadata passes.
	res, err := v.Validate(ctx, "https://cdn.example.com/uploads/item.png", goodMeta)
	if err != nil {
		t.Fatalf("expected valid image, got %v", err)
	}
	if res.ContentType != "image/png" || res.SizeBytes != 1024 {
		t.Fatalf("unexpected validation result: %+v", res)
	}

	// Oversized rejected.
	if _, err := v.Validate(ctx, "https://cdn.example.com/uploads/item.png", map[string]any{
		"content_type": "image/png",
		"size_bytes":   DefaultMaxImageBytes + 1,
	}); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected oversized image rejection, got %v", err)
	}

	// Bad MIME rejected.
	if _, err := v.Validate(ctx, "https://cdn.example.com/uploads/item.png", map[string]any{
		"content_type": "image/svg+xml",
		"size_bytes":   100,
	}); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected svg mime rejection, got %v", err)
	}
}

func TestImageValidatorDNSRebindingSSRFBlocked(t *testing.T) {
	ctx := context.Background()
	v := NewImageValidator(ImageValidatorConfig{
		Mode: ModeStrict,
		LookupIPAddr: func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		},
	})

	_, err := v.Validate(ctx, "https://evil-rebind.example.com/pic.png", nil)
	if !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected DNS-resolved metadata IP to be blocked, got %v", err)
	}

	// Also verify IsBlockedIP directly on IPv4-mapped IPv6 and CGNAT.
	for _, ipStr := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "::1", "::ffff:10.0.0.1", "fe80::1", "0.0.0.0"} {
		if !IsBlockedIP(netip.MustParseAddr(ipStr)) {
			t.Errorf("expected %s to be blocked", ipStr)
		}
	}
	if IsBlockedIP(netip.MustParseAddr("8.8.8.8")) {
		t.Errorf("expected public IP 8.8.8.8 to be allowed")
	}
}

func TestImageValidatorHybridAndStrictProbe(t *testing.T) {
	ctx := context.Background()
	pngBytes := makeTestPNG(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(pngBytes)))
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pngBytes)
		case "/fake.png":
			// Disguised HTML pretending to be image/png -> magic byte sniff MUST reject!
			w.Header().Set("Content-Type", "image/png")
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Length", "32")
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
		case "/huge.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.FormatInt(10*1024*1024, 10))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	v := NewImageValidator(ImageValidatorConfig{
		Mode:         ModeHybrid,
		MaxBytes:     2 * 1024 * 1024,
		AllowedHosts: []string{u.Host},
	})

	// Missing metadata triggers HTTP probe -> succeeds on real PNG.
	res, err := v.Validate(ctx, srv.URL+"/ok.png", nil)
	if err != nil {
		t.Fatalf("expected hybrid probe to pass for real png, got %v", err)
	}
	if res.ContentType != "image/png" || res.SizeBytes != int64(len(pngBytes)) {
		t.Fatalf("unexpected probed result: %+v", res)
	}

	// Disguised HTML rejected by magic byte sniff.
	if _, err := v.Validate(ctx, srv.URL+"/fake.png", nil); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected disguised HTML to fail magic sniff, got %v", err)
	}

	// Oversized rejected by HEAD Content-Length.
	if _, err := v.Validate(ctx, srv.URL+"/huge.png", nil); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected oversized image to be rejected, got %v", err)
	}

	// Without AllowedHosts, loopback httptest server is blocked by SSRF policy.
	vNoAllow := NewImageValidator(ImageValidatorConfig{Mode: ModeStrict})
	if _, err := vNoAllow.Validate(ctx, srv.URL+"/ok.png", nil); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected loopback server without AllowedHosts to be blocked, got %v", err)
	}
}

func TestDetectExternalContactWarning(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Shop oi san pham nay con hang khong?", false},
		{"Xem tai https://bach-dang.nexusmart.localhost/p/123", false},
		{"Link tren https://nexusmart.vn/products/abc", false},
		{"Goi cho minh so 0901234567 nhe", true},
		{"Nhan zalo +84 912.345.678 de giam gia", true},
		{"Vao zalo.me/0901234567 nhe", true},
		{"Mua o https://external-scam.example.com/deal", true},
	}
	for _, tc := range cases {
		if got := DetectExternalContactWarning(tc.text, "nexusmart.localhost"); got != tc.want {
			t.Errorf("DetectExternalContactWarning(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
