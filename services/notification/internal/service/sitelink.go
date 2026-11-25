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

	"github.com/nexusmart/core/services/notification/internal/store"
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
// It preserves checkout_group_id resolution (single-shop group -> sub-order detail, multi-shop
// or unseen checkout group -> "<site>/don-hang", never "/don-hang/<group_id>").
// Lookup errors are logged and never fail notification delivery.
func (s *NotificationService) resolveOrderURL(ctx context.Context, orderID, orderGroupID, storefrontShopID string) string {
	orderID = strings.TrimSpace(orderID)
	orderGroupID = strings.TrimSpace(orderGroupID)
	shopID := strings.TrimSpace(storefrontShopID)
	if s.siteResolver == nil || (orderID == "" && orderGroupID == "" && shopID == "") {
		return ""
	}

	targetOrderID := ""
	listOnly := false

	// 1. Distinct order_id and order_group_id means the event already carries a real sub-order ID.
	if orderID != "" && orderGroupID != "" && orderID != orderGroupID {
		targetOrderID = orderID
		if shopID == "" {
			if part, found, err := s.repo.GetOrderParticipant(ctx, orderID); err == nil && found {
				shopID = strings.TrimSpace(part.StorefrontShopID)
			}
		}
	} else {
		// 2. If orderID matches a known sub-order participant directly, link straight to it.
		foundParticipant := false
		if orderID != "" {
			if part, found, err := s.repo.GetOrderParticipant(ctx, orderID); err == nil && found {
				foundParticipant = true
				targetOrderID = orderID
				if shopID == "" {
					shopID = strings.TrimSpace(part.StorefrontShopID)
				}
			}
		}
		if !foundParticipant {
			// 3. Look up sub-orders belonging to checkout_group_id (orderGroupID, or orderID when group ID is absent).
			groupLookup := orderGroupID
			if groupLookup == "" {
				groupLookup = orderID
			}
			var parts []store.OrderParticipant
			if groupLookup != "" {
				if listed, err := s.repo.ListOrderParticipantsByGroup(ctx, groupLookup); err == nil {
					parts = listed
				}
			}
			switch len(parts) {
			case 1:
				targetOrderID = parts[0].OrderID
				if shopID == "" {
					shopID = strings.TrimSpace(parts[0].StorefrontShopID)
				}
			case 0:
				// 4. Never emit /don-hang/<checkout_group_id> when orderID is missing or known to be checkout_group_id.
				if orderID == "" || (orderGroupID != "" && orderID == orderGroupID) {
					listOnly = true
				} else {
					targetOrderID = orderID
				}
			default:
				// Multi-shop checkout group: link to the site's order list rather than a 404 checkout_group_id URL.
				listOnly = true
				if shopID == "" {
					shopID = commonStorefrontShopID(parts)
				}
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
	if listOnly || targetOrderID == "" {
		return s.siteLinks.siteOrdersListURL(host)
	}
	return s.siteLinks.siteOrderURL(host, targetOrderID)
}

func commonStorefrontShopID(parts []store.OrderParticipant) string {
	common := ""
	for _, part := range parts {
		sid := strings.TrimSpace(part.StorefrontShopID)
		if sid == "" {
			continue
		}
		if common == "" {
			common = sid
		} else if common != sid {
			return ""
		}
	}
	return common
}

// applyDeliveryOrderLink sets the buyer in-app link and appends the link to the buyer email body.
// An empty link leaves the notification without a link (no platform page to point to).
func applyDeliveryOrderLink(d *recipientDelivery, link string) {
	if d.inApp != nil {
		d.inApp.Link = link
	}
	if d.email != nil && link != "" {
		d.email.Body += "Xem chi tiết đơn hàng: " + link + "\n"
	}
}
