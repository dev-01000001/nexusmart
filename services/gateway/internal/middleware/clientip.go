package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	defaultProxyRefreshTTL = 30 * time.Second
	defaultProxyRetryTTL   = 2 * time.Second
	defaultProxyMissTTL    = 5 * time.Second
	defaultDNSLookupLimit  = time.Second
)

// TrustedProxies matches direct TCP peer IPs against configured CIDRs, literal IPs,
// and periodically resolved hostnames (e.g. Docker Compose service name "web").
// A nil or empty TrustedProxies trusts no proxy and always uses RemoteAddr.
type TrustedProxies struct {
	prefixes []netip.Prefix
	hosts    []string

	lookupFn   func(ctx context.Context, host string) ([]netip.Addr, error)
	nowFn      func() time.Time
	refreshTTL time.Duration
	retryTTL   time.Duration
	missTTL    time.Duration
	log        *slog.Logger

	refreshMu   sync.Mutex
	mu          sync.RWMutex
	resolved    map[netip.Addr]struct{}
	lastRefresh time.Time
	allResolved bool
}

// NewTrustedProxies parses a comma-separated list of CIDRs, IPs, and hostnames.
// Empty input returns a TrustedProxies that trusts nothing (RemoteAddr only).
func NewTrustedProxies(raw string, log *slog.Logger) *TrustedProxies {
	tp, _ := newTrustedProxies(raw, log, false)
	return tp
}

// NewTrustedProxiesValidated parses a comma-separated list of CIDRs, literal IPs,
// and hostnames, failing fast when any CIDR entry (containing '/') or hostname token is invalid.
func NewTrustedProxiesValidated(raw string, log *slog.Logger) (*TrustedProxies, error) {
	return newTrustedProxies(raw, log, true)
}

// ParseTrustedProxies parses a comma-separated list of strict CIDRs (e.g. "172.31.250.0/28,10.0.0.0/8").
// Empty input returns a TrustedProxies that trusts no proxy headers; non-CIDR entries return an error.
func ParseTrustedProxies(raw string) (*TrustedProxies, error) {
	tp := newEmptyTrustedProxies(nil)
	for _, item := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(item)
		if entry == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
		}
		tp.prefixes = append(tp.prefixes, normalizePrefix(pfx))
	}
	return tp, nil
}

func newEmptyTrustedProxies(log *slog.Logger) *TrustedProxies {
	return &TrustedProxies{
		lookupFn: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		nowFn:      time.Now,
		refreshTTL: defaultProxyRefreshTTL,
		retryTTL:   defaultProxyRetryTTL,
		missTTL:    defaultProxyMissTTL,
		log:        log,
		resolved:   make(map[netip.Addr]struct{}),
	}
}

func newTrustedProxies(raw string, log *slog.Logger, strict bool) (*TrustedProxies, error) {
	tp := newEmptyTrustedProxies(log)
	seenHosts := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(item)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			pfx, err := netip.ParsePrefix(entry)
			if err != nil {
				if strict {
					return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
				}
				continue
			}
			tp.prefixes = append(tp.prefixes, normalizePrefix(pfx))
			continue
		}
		if addr, ok := parseIP(entry); ok {
			tp.prefixes = append(tp.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		if strict && !isValidProxyHostname(entry) {
			return nil, fmt.Errorf("invalid trusted proxy entry %q", entry)
		}
		lower := strings.ToLower(entry)
		if _, dup := seenHosts[lower]; !dup {
			seenHosts[lower] = struct{}{}
			tp.hosts = append(tp.hosts, entry)
		}
	}

	if len(tp.hosts) > 0 {
		tp.refresh(false)
	}
	return tp, nil
}

func isValidProxyHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// Contains reports whether addr belongs to the configured trusted proxy set.
func (tp *TrustedProxies) Contains(addr netip.Addr) bool {
	return tp.IsTrusted(addr)
}

// IsTrusted reports whether addr belongs to the configured trusted proxy set.
func (tp *TrustedProxies) IsTrusted(addr netip.Addr) bool {
	if tp == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, pfx := range tp.prefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	if len(tp.hosts) == 0 {
		return false
	}

	now := tp.nowFn()
	tp.mu.RLock()
	_, hit := tp.resolved[addr]
	last := tp.lastRefresh
	allResolved := tp.allResolved
	tp.mu.RUnlock()

	ttl := tp.refreshTTL
	if !allResolved {
		ttl = tp.retryTTL
	}
	if hit && now.Sub(last) < ttl {
		return true
	}

	if now.Sub(last) >= ttl || (!hit && now.Sub(last) >= tp.missTTL) {
		tp.refresh(!hit)
		tp.mu.RLock()
		_, hit = tp.resolved[addr]
		tp.mu.RUnlock()
	}
	return hit
}

func (tp *TrustedProxies) refresh(onMiss bool) {
	tp.refreshMu.Lock()
	defer tp.refreshMu.Unlock()

	now := tp.nowFn()
	tp.mu.RLock()
	last := tp.lastRefresh
	allResolved := tp.allResolved
	tp.mu.RUnlock()

	minInterval := tp.refreshTTL
	if !allResolved {
		minInterval = tp.retryTTL
	} else if onMiss {
		minInterval = tp.missTTL
	}
	if !last.IsZero() && now.Sub(last) < minInterval {
		return
	}

	next := make(map[netip.Addr]struct{}, len(tp.hosts))
	resolvedAll := true
	for _, host := range tp.hosts {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDNSLookupLimit)
		addrs, err := tp.lookupFn(ctx, host)
		cancel()
		if err != nil || len(addrs) == 0 {
			resolvedAll = false
			if err != nil && tp.log != nil {
				tp.log.Warn("trusted proxy hostname lookup failed", slog.String("host", host), slog.String("error", err.Error()))
			}
			continue
		}
		for _, a := range addrs {
			if a.IsValid() {
				next[a.Unmap()] = struct{}{}
			}
		}
	}

	tp.mu.Lock()
	tp.resolved = next
	tp.lastRefresh = tp.nowFn()
	tp.allResolved = resolvedAll
	tp.mu.Unlock()
}

// ClientIP returns the caller's IP address.
// Forwarded headers (X-Forwarded-For, X-Real-IP) are ignored unless RemoteAddr belongs to tp.
// When X-Forwarded-For is trusted, hops are scanned right-to-left and the nearest untrusted IP is returned.
func (tp *TrustedProxies) ClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	rawPeer := strings.TrimSpace(r.RemoteAddr)
	if rawPeer == "" {
		return "unknown"
	}

	peerAddr, peerValid := parseIP(rawPeer)
	peerStr := rawPeer
	if peerValid {
		peerStr = peerAddr.String()
	} else if host, _, err := net.SplitHostPort(rawPeer); err == nil && host != "" {
		peerStr = host
	}

	if tp == nil || !peerValid || !tp.IsTrusted(peerAddr) {
		return peerStr
	}

	hops := parseXFFHops(r.Header.Values("X-Forwarded-For"))
	if len(hops) > 0 {
		var leftmostValid netip.Addr
		for i := len(hops) - 1; i >= 0; i-- {
			addr, ok := parseIP(hops[i])
			if !ok {
				// Stop trusting further left if an untrusted/malformed token appears.
				return peerStr
			}
			if !tp.IsTrusted(addr) {
				return addr.String()
			}
			leftmostValid = addr
		}
		if leftmostValid.IsValid() {
			return leftmostValid.String()
		}
		return peerStr
	}

	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if addr, ok := parseIP(xri); ok {
			return addr.String()
		}
	}

	return peerStr
}

// TrustedPeerIP returns the normalized direct TCP peer IP and true when r.RemoteAddr
// belongs to tp. It never inspects forwarded headers.
func (tp *TrustedProxies) TrustedPeerIP(r *http.Request) (string, bool) {
	if tp == nil || r == nil {
		return "", false
	}
	peerAddr, ok := parseIP(r.RemoteAddr)
	if !ok || !tp.IsTrusted(peerAddr) {
		return "", false
	}
	return peerAddr.String(), true
}

// ClientIP extracts the direct peer IP from RemoteAddr without trusting any forwarded headers.
// Use (*TrustedProxies).ClientIP when running behind configured reverse proxies.
func ClientIP(r *http.Request) string {
	return (*TrustedProxies)(nil).ClientIP(r)
}

func parseXFFHops(values []string) []string {
	var hops []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				hops = append(hops, p)
			}
		}
	}
	return hops
}

func parseIP(raw string) (netip.Addr, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(s); err == nil && host != "" {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap(), true
		}
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if trimmed != s {
		if addr, err := netip.ParseAddr(trimmed); err == nil {
			return addr.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

func normalizePrefix(p netip.Prefix) netip.Prefix {
	addr := p.Addr()
	if addr.Is4In6() {
		bits := p.Bits() - 96
		if bits < 0 {
			bits = 0
		}
		return netip.PrefixFrom(addr.Unmap(), bits).Masked()
	}
	return p.Masked()
}
