package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const shippingLabelHTMLCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"

// stripSecurityHeaders removes upstream security headers so gateway sets a single
// canonical value for each security header without duplicates from ReverseProxy copyHeader.
func stripSecurityHeaders(h http.Header) {
	h.Del("X-Content-Type-Options")
	h.Del("X-Frame-Options")
	h.Del("Referrer-Policy")
	h.Del("Content-Security-Policy")
}

// NewWebhookProxy creates a reverse proxy that forwards webhooks to targetBaseURL while preserving
// the exact raw request body, X-Signature, X-Timestamp, and W3C traceparent headers.
func NewWebhookProxy(targetBaseURL string, log *slog.Logger) (http.Handler, error) {
	target, err := url.Parse(strings.TrimSpace(targetBaseURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid webhook upstream url %q: %w", targetBaseURL, err)
	}

	baseTransport, _ := http.DefaultTransport.(*http.Transport)
	var transport http.RoundTripper
	if baseTransport != nil {
		tr := baseTransport.Clone()
		tr.ResponseHeaderTimeout = 15 * time.Second
		transport = tr
	}

	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			pr.SetXForwarded()

			// Preserve incoming signature/timestamp headers verbatim and propagate traceparent.
			if sig := pr.In.Header.Get("X-Signature"); sig != "" {
				pr.Out.Header.Set("X-Signature", sig)
			}
			if ts := pr.In.Header.Get("X-Timestamp"); ts != "" {
				pr.Out.Header.Set("X-Timestamp", ts)
			}
			otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ModifyResponse: func(resp *http.Response) error {
			stripSecurityHeaders(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			var maxErr *http.MaxBytesError
			if errors.As(proxyErr, &maxErr) {
				httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
				return
			}
			if log != nil {
				log.ErrorContext(r.Context(), "webhook proxy error", slog.String("path", r.URL.Path), slog.String("error", proxyErr.Error()))
			}
			httpx.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "upstream webhook service unavailable")
		},
	}

	return rp, nil
}

// NewSSEProxy creates a flush-enabled reverse proxy for payment SSE events:
// GET /api/v1/payments/{id}/events -> PAYMENT_HTTP_URL/payments/{id}/events.
func NewSSEProxy(paymentHTTPURL string, log *slog.Logger) (http.Handler, error) {
	target, err := url.Parse(strings.TrimSpace(paymentHTTPURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid payment SSE upstream url %q: %w", paymentHTTPURL, err)
	}

	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			rewritten := strings.TrimPrefix(pr.In.URL.Path, "/api/v1")
			if rewritten == "" {
				rewritten = "/"
			}
			pr.Out.URL.Path = rewritten
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			pr.SetXForwarded()

			pr.Out.Header.Del("X-User-Id")
			pr.Out.Header.Del("X-User-Role")
			pr.Out.Header.Del("X-Shop-Id")
			if rawAuth := auth.RawTokenFromContext(pr.In.Context()); rawAuth != "" {
				pr.Out.Header.Set("Authorization", rawAuth)
			}
			otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ModifyResponse: func(resp *http.Response) error {
			stripSecurityHeaders(resp.Header)
			resp.Header.Set("X-Accel-Buffering", "no")
			if resp.Header.Get("Cache-Control") == "" {
				resp.Header.Set("Cache-Control", "no-cache")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			if log != nil {
				log.ErrorContext(r.Context(), "payment SSE proxy error", slog.String("path", r.URL.Path), slog.String("error", proxyErr.Error()))
			}
			httpx.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "payment event stream unavailable")
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Disable per-request write timeout for long-lived SSE connections.
		clearWriteDeadline(w)
		rp.ServeHTTP(w, r)
	}), nil
}

// NewShippingLabelProxy creates a reverse proxy for authenticated shipping label retrieval (SHP-B8):
// GET /api/v1/shipping/labels/{tracking_code} -> SHIPPING_HTTP_URL/api/v1/shipping/labels/{tracking_code}.
// It forwards only the verified Authorization Bearer token and strips plain identity headers.
func NewShippingLabelProxy(shippingHTTPURL string, log *slog.Logger) (http.Handler, error) {
	target, err := url.Parse(strings.TrimSpace(shippingHTTPURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid shipping label upstream url %q: %w", shippingHTTPURL, err)
	}

	baseTransport, _ := http.DefaultTransport.(*http.Transport)
	var transport http.RoundTripper
	if baseTransport != nil {
		tr := baseTransport.Clone()
		tr.ResponseHeaderTimeout = 15 * time.Second
		transport = tr
	}

	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if pr.In.URL.Path == "/api/v1/shipping/labels/batch" {
				pr.Out.URL.Path = "/labels/batch"
				pr.Out.URL.RawPath = ""
			} else {
				pr.Out.URL.Path = pr.In.URL.Path
				pr.Out.URL.RawPath = pr.In.URL.RawPath
			}
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			pr.SetXForwarded()

			for k := range pr.Out.Header {
				lk := strings.ToLower(k)
				if strings.HasPrefix(lk, "x-user-") || lk == "x-shop-id" {
					pr.Out.Header.Del(k)
				}
			}
			if rawAuth := auth.RawTokenFromContext(pr.In.Context()); rawAuth != "" {
				pr.Out.Header.Set("Authorization", rawAuth)
			}
			otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ModifyResponse: func(resp *http.Response) error {
			stripSecurityHeaders(resp.Header)
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				resp.Header.Set("Content-Security-Policy", shippingLabelHTMLCSP)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			var maxErr *http.MaxBytesError
			if errors.As(proxyErr, &maxErr) {
				httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
				return
			}
			if log != nil {
				log.ErrorContext(r.Context(), "shipping label proxy error", slog.String("path", r.URL.Path), slog.String("error", proxyErr.Error()))
			}
			httpx.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "shipping label service unavailable")
		},
	}

	return rp, nil
}

func clearWriteDeadline(w http.ResponseWriter) {
	type unwrapper interface {
		Unwrap() http.ResponseWriter
	}
	type writeDeadliner interface {
		SetWriteDeadline(time.Time) error
	}
	cur := w
	for cur != nil {
		if wd, ok := cur.(writeDeadliner); ok {
			_ = wd.SetWriteDeadline(time.Time{})
			return
		}
		if u, ok := cur.(unwrapper); ok {
			cur = u.Unwrap()
			continue
		}
		return
	}
}
