package service

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nexusmart/core/services/notification/internal/userclient"
)

// siteOrderPath is the buyer order detail route served on a shop site host.
const siteOrderPath = "/don-hang/"

const defaultSiteLookupTimeout = 2 * time.Second

// hostnamePattern accepts lowercase DNS hostnames with at least one dot (e.g. "bach-dang.nexusmart.vn").
var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// SiteLinkConfig controls how absolute order links to shop sites are built.
type SiteLinkConfig struct {
	// Scheme is "https" (default) or "http" (local dev).
	Scheme string
	// Port is appended to the site host when non-empty (local dev, e.g. "3300").
	Port string
	// LookupTimeout bounds the UserService site lookup; defaults to 2s.
	LookupTimeout time.Duration
}

// Option customizes a NotificationService.
type Option func(*NotificationService)

// WithSiteLinks enables absolute order links on the site host where the order was placed.
// Without it (or when the order has no PUBLISHED site) order notifications carry no link.
func WithSiteLinks(resolver userclient.SiteResolver, cfg SiteLinkConfig) Option {
	return func(s *NotificationService) {
		scheme := strings.ToLower(strings.TrimSpace(cfg.Scheme))
		if scheme != "http" {
			scheme = "https"
		}
		port := strings.TrimSpace(cfg.Port)
		if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
			port = ""
		}
		timeout := cfg.LookupTimeout
		if timeout <= 0 {
			timeout = defaultSiteLookupTimeout
		}
		s.siteResolver = resolver
		s.siteLinks = SiteLinkConfig{Scheme: scheme, Port: port, LookupTimeout: timeout}
	}
}

// siteOrderURL builds "<scheme>://<host>[:port]/don-hang/<orderID>" or "" when host/order are unusable.
func (c SiteLinkConfig) siteOrderURL(host, orderID string) string {
	return c.sitePathURL(host, siteOrderPath, orderID)
}

// siteOrdersListURL builds "<scheme>://<host>[:port]/don-hang" for checkout-group payment notices.
func (c SiteLinkConfig) siteOrdersListURL(host string) string {
	return c.sitePathURL(host, strings.TrimSuffix(siteOrderPath, "/"), "")
}

func (c SiteLinkConfig) sitePathURL(host, basePath, orderID string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	orderID = strings.TrimSpace(orderID)
	if host == "" {
		return ""
	}
	if basePath == siteOrderPath && orderID == "" {
		return ""
	}
	if strings.ContainsAny(orderID, "/?#") {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		// primary_host is stored without a port; tolerate one but drop it in favor of config.
		host = h
	}
	if !hostnamePattern.MatchString(host) {
		return ""
	}
	if c.Port != "" {
		host = net.JoinHostPort(host, c.Port)
	}
	scheme := c.Scheme
	if scheme == "" {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: host, Path: basePath + orderID}
	return u.String()
}

// resolveOrderURL returns the absolute order link on the site where the order was placed,
// or "" for MARKETPLACE/legacy orders, shops without a PUBLISHED site, or lookup failures.
// When isOrderGroup is true (payment.* carrying checkout_group_id instead of a sub-order ID),
// the link points to "<site>/don-hang" unless orderID matches a known order participant.
// Lookup errors are logged and never fail notification delivery.
func (s *NotificationService) resolveOrderURL(ctx context.Context, orderID, storefrontShopID string, isOrderGroup bool) string {
	orderID = strings.TrimSpace(orderID)
	if s.siteResolver == nil || (orderID == "" && strings.TrimSpace(storefrontShopID) == "") {
		return ""
	}
	shopID := strings.TrimSpace(storefrontShopID)
	hasParticipant := false
	if orderID != "" {
		if part, found, err := s.repo.GetOrderParticipant(ctx, orderID); err == nil && found {
			hasParticipant = true
			if shopID == "" {
				shopID = strings.TrimSpace(part.StorefrontShopID)
			}
		}
	}
	if shopID == "" {
		return ""
	}

	lookupCtx, cancel := context.WithTimeout(ctx, s.siteLinks.LookupTimeout)
	defer cancel()
	host, err := s.siteResolver.GetSitePrimaryHost(lookupCtx, shopID)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to resolve site host for order link",
			slog.String("order_id", orderID),
			slog.String("shop_id", shopID),
			slog.String("error", err.Error()),
		)
		return ""
	}
	if (isOrderGroup && !hasParticipant) || orderID == "" {
		return s.siteLinks.siteOrdersListURL(host)
	}
	return s.siteLinks.siteOrderURL(host, orderID)
}

// applyOrderLink sets the in-app link and appends the link to the email body.
// An empty link leaves the notification without a link (no platform page to point to).
func applyOrderLink(plan *eventPlan, link string) {
	if plan.inApp != nil {
		plan.inApp.Link = link
	}
	if plan.email != nil && link != "" {
		plan.email.Body += "Xem chi tiết đơn hàng: " + link + "\n"
	}
}
