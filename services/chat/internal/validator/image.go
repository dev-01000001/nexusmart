package validator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ModeStrict   = "strict"
	ModeHybrid   = "hybrid"
	ModeMetadata = "metadata"

	DefaultMaxImageBytes int64 = 5 * 1024 * 1024 // 5 MiB
	maxProbeRedirects          = 3
	magicSniffBytes            = 512
)

// ErrInvalidImage is returned when an image message URL, type, size, or SSRF check fails.
var ErrInvalidImage = errors.New("invalid image attachment")

var (
	allowedImageExtensions = map[string]struct{}{
		".jpg":  {},
		".jpeg": {},
		".png":  {},
		".webp": {},
		".gif":  {},
	}

	allowedImageMIMEs = map[string]struct{}{
		"image/jpeg": {},
		"image/png":  {},
		"image/webp": {},
		"image/gif":  {},
	}

	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

	vnPhonePattern = regexp.MustCompile(`(?i)(?:^|[^\d])(?:\+?84|0)[\s.\-]*(?:3|5|7|8|9)(?:[\s.\-]*\d){8}(?:$|[^\d])`)
	urlPattern     = regexp.MustCompile(`(?i)(?:https?://[^\s<>"]+|(?:^|[\s(])(?:www\.|zalo\.me/|t\.me/|fb\.com/|facebook\.com/)[^\s<>"]+)`)
)

// LookupIPAddrFunc allows injecting a deterministic DNS resolver in tests.
type LookupIPAddrFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// ImageValidatorConfig configures SSRF-safe image attachment validation.
type ImageValidatorConfig struct {
	Mode         string // strict | hybrid | metadata (defaults to hybrid)
	MaxBytes     int64  // defaults to 5 MiB
	AllowedHosts []string
	HTTPTimeout  time.Duration
	LookupIPAddr LookupIPAddrFunc
}

// ImageValidationResult contains validated image attributes.
type ImageValidationResult struct {
	ContentType string
	SizeBytes   int64
}

// ImageValidator validates chat image URLs against SSRF, type, and size constraints.
type ImageValidator struct {
	mode         string
	maxBytes     int64
	allowedHosts map[string]struct{}
	lookupIPAddr LookupIPAddrFunc
	client       *http.Client
}

// NewImageValidator constructs an SSRF-protected ImageValidator.
func NewImageValidator(cfg ImageValidatorConfig) *ImageValidator {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case ModeStrict, ModeHybrid, ModeMetadata:
	default:
		mode = ModeHybrid
	}

	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxImageBytes
	}

	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	allowed := make(map[string]struct{}, len(cfg.AllowedHosts))
	for _, h := range cfg.AllowedHosts {
		if normalized := strings.ToLower(strings.TrimSpace(h)); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}

	lookup := cfg.LookupIPAddr
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}

	v := &ImageValidator{
		mode:         mode,
		maxBytes:     maxBytes,
		allowedHosts: allowed,
		lookupIPAddr: lookup,
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 15 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 nil, // Never inherit HTTP_PROXY for SSRF-checked probes.
		DialContext:           v.safeDialContext(dialer),
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}

	v.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxProbeRedirects {
				return fmt.Errorf("%w: too many redirects", ErrInvalidImage)
			}
			if _, err := v.validateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}

	return v
}

// Validate checks an image URL and optional metadata according to the configured mode.
func (v *ImageValidator) Validate(ctx context.Context, rawURL string, metadata map[string]any) (ImageValidationResult, error) {
	parsedURL, err := v.validateURL(rawURL)
	if err != nil {
		return ImageValidationResult{}, err
	}

	if ext := strings.ToLower(path.Ext(parsedURL.Path)); ext != "" {
		if _, ok := allowedImageExtensions[ext]; !ok {
			return ImageValidationResult{}, fmt.Errorf("%w: disallowed image extension %q", ErrInvalidImage, ext)
		}
	}

	metaCT, hasCT := extractStringMeta(metadata, "content_type")
	metaSize, hasSize, badSize := extractSizeMeta(metadata, "size_bytes")
	if badSize {
		return ImageValidationResult{}, fmt.Errorf("%w: size_bytes must be a positive integer", ErrInvalidImage)
	}
	if hasSize && (metaSize <= 0 || metaSize > v.maxBytes) {
		return ImageValidationResult{}, fmt.Errorf("%w: image size %d exceeds allowed range (1..%d bytes)", ErrInvalidImage, metaSize, v.maxBytes)
	}
	if hasCT {
		normalizedCT, ctErr := validateImageMIME(metaCT)
		if ctErr != nil {
			return ImageValidationResult{}, ctErr
		}
		metaCT = normalizedCT
	}

	switch v.mode {
	case ModeMetadata:
		if !hasCT || !hasSize {
			return ImageValidationResult{}, fmt.Errorf("%w: metadata.content_type and metadata.size_bytes are required", ErrInvalidImage)
		}
		return ImageValidationResult{ContentType: metaCT, SizeBytes: metaSize}, nil

	case ModeHybrid:
		if hasCT && hasSize {
			return ImageValidationResult{ContentType: metaCT, SizeBytes: metaSize}, nil
		}
		return v.probeURL(ctx, parsedURL.String())

	default: // ModeStrict
		return v.probeURL(ctx, parsedURL.String())
	}
}

func (v *ImageValidator) validateURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("%w: image url is required", ErrInvalidImage)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed url: %w", ErrInvalidImage, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: only http and https urls are allowed", ErrInvalidImage)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: url must not contain userinfo", ErrInvalidImage)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("%w: url host is required", ErrInvalidImage)
	}

	if v.isHostAllowed(host, u.Host) {
		return u, nil
	}

	if isBlockedHostname(host) {
		return nil, fmt.Errorf("%w: host %q is blocked by SSRF policy", ErrInvalidImage, host)
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if IsBlockedIP(addr) {
			return nil, fmt.Errorf("%w: ip %q is blocked by SSRF policy", ErrInvalidImage, host)
		}
	}
	return u, nil
}

func (v *ImageValidator) isHostAllowed(hostname, hostPort string) bool {
	if _, ok := v.allowedHosts[strings.ToLower(hostname)]; ok {
		return true
	}
	if _, ok := v.allowedHosts[strings.ToLower(hostPort)]; ok {
		return true
	}
	return false
}

func (v *ImageValidator) safeDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid dial address %q", ErrInvalidImage, addr)
		}
		lowerHost := strings.ToLower(strings.TrimSpace(host))
		if v.isHostAllowed(lowerHost, addr) {
			return dialer.DialContext(ctx, network, addr)
		}
		if isBlockedHostname(lowerHost) {
			return nil, fmt.Errorf("%w: host %q is blocked by SSRF policy", ErrInvalidImage, lowerHost)
		}

		// Literal IP check.
		if ip, parseErr := netip.ParseAddr(lowerHost); parseErr == nil {
			if IsBlockedIP(ip) {
				return nil, fmt.Errorf("%w: ip %q is blocked by SSRF policy", ErrInvalidImage, lowerHost)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		}

		// Resolve DNS and validate EVERY returned IP to defeat DNS rebinding / dual-record SSRF.
		ips, err := v.lookupIPAddr(ctx, lowerHost)
		if err != nil {
			return nil, fmt.Errorf("%w: dns lookup failed for %q: %w", ErrInvalidImage, lowerHost, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: no ip addresses resolved for %q", ErrInvalidImage, lowerHost)
		}

		var safeIP netip.Addr
		for _, ipAddr := range ips {
			parsed, ok := netip.AddrFromSlice(ipAddr.IP)
			if !ok {
				return nil, fmt.Errorf("%w: invalid resolved ip for %q", ErrInvalidImage, lowerHost)
			}
			if IsBlockedIP(parsed) {
				return nil, fmt.Errorf("%w: resolved ip %s for %q is blocked by SSRF policy", ErrInvalidImage, parsed.String(), lowerHost)
			}
			if !safeIP.IsValid() {
				safeIP = parsed.Unmap()
			}
		}

		return dialer.DialContext(ctx, network, net.JoinHostPort(safeIP.String(), port))
	}
}

func (v *ImageValidator) probeURL(ctx context.Context, targetURL string) (ImageValidationResult, error) {
	var (
		headSize int64 = -1
		headCT   string
	)

	// Step 1: lightweight HEAD probe to reject oversized files before transferring bytes.
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
	if err != nil {
		return ImageValidationResult{}, fmt.Errorf("%w: create head request: %w", ErrInvalidImage, err)
	}
	headResp, err := v.client.Do(headReq)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(headResp.Body, 4096))
		_ = headResp.Body.Close()

		if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
			if headResp.ContentLength > 0 {
				if headResp.ContentLength > v.maxBytes {
					return ImageValidationResult{}, fmt.Errorf("%w: image size %d exceeds maximum %d bytes", ErrInvalidImage, headResp.ContentLength, v.maxBytes)
				}
				headSize = headResp.ContentLength
			}
			if rawCT := strings.TrimSpace(headResp.Header.Get("Content-Type")); rawCT != "" {
				if ct, ctErr := validateImageMIME(rawCT); ctErr == nil {
					headCT = ct
				} else {
					return ImageValidationResult{}, ctErr
				}
			}
		}
	} else if errors.Is(err, ErrInvalidImage) {
		return ImageValidationResult{}, err
	}

	// Step 2: 512-byte Range GET to sniff magic bytes and confirm size.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return ImageValidationResult{}, fmt.Errorf("%w: create range get request: %w", ErrInvalidImage, err)
	}
	getReq.Header.Set("Range", fmt.Sprintf("bytes=0-%d", magicSniffBytes-1))

	getResp, err := v.client.Do(getReq)
	if err != nil {
		if errors.Is(err, ErrInvalidImage) {
			return ImageValidationResult{}, err
		}
		return ImageValidationResult{}, fmt.Errorf("%w: probe image url: %w", ErrInvalidImage, err)
	}
	defer func() { _ = getResp.Body.Close() }()

	if getResp.StatusCode != http.StatusOK && getResp.StatusCode != http.StatusPartialContent {
		return ImageValidationResult{}, fmt.Errorf("%w: image url returned HTTP %d", ErrInvalidImage, getResp.StatusCode)
	}

	if rawCT := strings.TrimSpace(getResp.Header.Get("Content-Type")); rawCT != "" {
		if _, ctErr := validateImageMIME(rawCT); ctErr != nil {
			return ImageValidationResult{}, ctErr
		}
	}

	headerBuf := make([]byte, magicSniffBytes)
	n, readErr := io.ReadFull(getResp.Body, headerBuf)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return ImageValidationResult{}, fmt.Errorf("%w: read image header: %w", ErrInvalidImage, readErr)
	}
	if n == 0 {
		return ImageValidationResult{}, fmt.Errorf("%w: image response body is empty", ErrInvalidImage)
	}
	sniffedCT, ctErr := validateImageMIME(http.DetectContentType(headerBuf[:n]))
	if ctErr != nil {
		return ImageValidationResult{}, fmt.Errorf("%w: magic byte check failed (%w)", ErrInvalidImage, ctErr)
	}
	if headCT != "" && headCT != sniffedCT {
		// Still an allowed image MIME, prefer sniffed magic bytes.
		headCT = sniffedCT
	} else if headCT == "" {
		headCT = sniffedCT
	}

	size := headSize
	if size <= 0 {
		if total, ok := parseContentRangeTotal(getResp.Header.Get("Content-Range")); ok {
			size = total
		}
	}
	if size <= 0 && getResp.StatusCode == http.StatusOK {
		if getResp.ContentLength > 0 {
			size = getResp.ContentLength
		} else {
			// Server ignored Range and omitted Content-Length: count safely up to MaxBytes+1.
			remaining, countErr := io.Copy(io.Discard, io.LimitReader(getResp.Body, v.maxBytes+1-int64(n)))
			if countErr != nil {
				return ImageValidationResult{}, fmt.Errorf("%w: count image body: %w", ErrInvalidImage, countErr)
			}
			size = int64(n) + remaining
		}
	}
	if size <= 0 {
		size = int64(n)
	}
	if size > v.maxBytes {
		return ImageValidationResult{}, fmt.Errorf("%w: image size %d exceeds maximum %d bytes", ErrInvalidImage, size, v.maxBytes)
	}

	return ImageValidationResult{
		ContentType: headCT,
		SizeBytes:   size,
	}, nil
}

// IsBlockedIP reports whether ip falls into loopback, private, link-local, metadata,
// CGNAT, multicast, or unspecified address ranges.
func IsBlockedIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip.Is4() && cgnatPrefix.Contains(ip) {
		return true
	}
	return false
}

func isBlockedHostname(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" || h == "localhost" || h == "metadata" || h == "metadata.google.internal" || h == "metadata.internal" {
		return true
	}
	if strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	return false
}

func validateImageMIME(raw string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("%w: invalid content_type %q", ErrInvalidImage, raw)
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if _, ok := allowedImageMIMEs[mediaType]; !ok {
		return "", fmt.Errorf("%w: disallowed image content_type %q", ErrInvalidImage, mediaType)
	}
	return mediaType, nil
}

func parseContentRangeTotal(header string) (int64, bool) {
	// Example: "bytes 0-511/10240"
	slash := strings.LastIndexByte(header, '/')
	if slash < 0 || slash == len(header)-1 {
		return 0, false
	}
	raw := strings.TrimSpace(header[slash+1:])
	if raw == "*" {
		return 0, false
	}
	total, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

func extractStringMeta(meta map[string]any, key string) (string, bool) {
	if len(meta) == 0 {
		return "", false
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	return s, s != ""
}

func extractSizeMeta(meta map[string]any, key string) (size int64, present bool, invalid bool) {
	if len(meta) == 0 {
		return 0, false, false
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return 0, false, false
	}
	switch v := raw.(type) {
	case int:
		return int64(v), true, v <= 0
	case int32:
		return int64(v), true, v <= 0
	case int64:
		return v, true, v <= 0
	case float64:
		if v <= 0 || v != float64(int64(v)) {
			return 0, true, true
		}
		return int64(v), true, false
	case json.Number:
		n, err := v.Int64()
		if err != nil || n <= 0 {
			return 0, true, true
		}
		return n, true, false
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return 0, true, true
		}
		return n, true, false
	default:
		return 0, true, true
	}
}

// DetectExternalContactWarning checks whether text contains a Vietnamese phone number
// or a link pointing outside the platform domain (UC-B14 non-blocking off-platform warning).
func DetectExternalContactWarning(text, siteRootDomain string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if vnPhonePattern.MatchString(text) {
		return true
	}

	allowedDomains := []string{"nexusmart.vn", "nexusmart.local", "nexusmart.localhost"}
	if root := strings.ToLower(strings.TrimSpace(siteRootDomain)); root != "" {
		allowedDomains = append(allowedDomains, root)
	}

	matches := urlPattern.FindAllString(text, -1)
	for _, m := range matches {
		candidate := strings.TrimSpace(m)
		candidate = strings.TrimLeft(candidate, "(")
		candidate = strings.TrimRight(candidate, ".,);!?")
		if !strings.HasPrefix(strings.ToLower(candidate), "http://") && !strings.HasPrefix(strings.ToLower(candidate), "https://") {
			candidate = "https://" + candidate
		}
		u, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if host == "" {
			continue
		}
		if !isPlatformHost(host, allowedDomains) {
			return true
		}
	}
	return false
}

func isPlatformHost(host string, allowedDomains []string) bool {
	for _, d := range allowedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
