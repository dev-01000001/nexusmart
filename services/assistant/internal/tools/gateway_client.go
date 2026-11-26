package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// ErrMissingBearerToken is returned when a user-scoped Gateway call is attempted without an end-user JWT.
var ErrMissingBearerToken = errors.New("missing end-user bearer token")

// GatewayClient defines authenticated end-user calls routed through the NexusMart API Gateway.
// Every method MUST forward ONLY the end-user's rawBearer JWT — never a service/admin token.
type GatewayClient interface {
	ListBuyerOrders(ctx context.Context, rawBearer, storefrontShopID, status string) ([]map[string]any, error)
	GetOrder(ctx context.Context, rawBearer, orderID string) (map[string]any, error)
	AddCartItem(ctx context.Context, rawBearer, skuID string, qty int32) (map[string]any, error)
	ListSellerOrders(ctx context.Context, rawBearer string) ([]map[string]any, error)
	GetSellerBalance(ctx context.Context, rawBearer string) (map[string]any, error)
	ListSellerProducts(ctx context.Context, rawBearer string) ([]map[string]any, error)
	CreateChatConversation(ctx context.Context, rawBearer, shopID string) (map[string]any, error)
}

// HTTPGatewayClient calls NexusMart API Gateway over HTTP using strictly the caller's JWT.
type HTTPGatewayClient struct {
	baseURL string
	host    string
	client  *http.Client
	timeout time.Duration
	cb      *resilience.CircuitBreaker
}

// NewHTTPGatewayClient validates baseURL against SSRF and builds an HTTP Gateway client.
func NewHTTPGatewayClient(baseURL string, timeout time.Duration, cb *resilience.CircuitBreaker) (*HTTPGatewayClient, error) {
	trimmed := strings.TrimSpace(baseURL)
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse gateway baseURL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("invalid gateway baseURL %q: scheme must be http or https with non-empty host", baseURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("invalid gateway baseURL %q: embedded credentials not allowed", baseURL)
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if cb == nil {
		cb = resilience.NewCircuitBreaker(5, 15*time.Second)
	}

	return &HTTPGatewayClient{
		baseURL: strings.TrimRight(trimmed, "/"),
		host:    u.Host,
		timeout: timeout,
		cb:      cb,
		client: &http.Client{
			Timeout: timeout + time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// ListBuyerOrders calls GET /api/v1/orders using the buyer's JWT.
func (c *HTTPGatewayClient) ListBuyerOrders(ctx context.Context, rawBearer, storefrontShopID, status string) ([]map[string]any, error) {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("page_size", "20")
	if s := strings.TrimSpace(storefrontShopID); s != "" {
		q.Set("storefront_shop_id", s)
	}
	if st := strings.TrimSpace(status); st != "" {
		q.Set("status", st)
	}
	body, err := c.doJSON(ctx, http.MethodGet, "/api/v1/orders?"+q.Encode(), rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	return decodeItemsList(body)
}

// GetOrder calls GET /api/v1/orders/{id} using the buyer's JWT.
func (c *HTTPGatewayClient) GetOrder(ctx context.Context, rawBearer, orderID string) (map[string]any, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("order_id is required")
	}
	body, err := c.doJSON(ctx, http.MethodGet, "/api/v1/orders/"+url.PathEscape(orderID), rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	obj, err := decodeMap(body)
	if err != nil {
		return nil, err
	}
	if inner, ok := obj["order"].(map[string]any); ok {
		return inner, nil
	}
	return obj, nil
}

// AddCartItem calls POST /api/v1/cart/items using the buyer's JWT. Never retries POST.
func (c *HTTPGatewayClient) AddCartItem(ctx context.Context, rawBearer, skuID string, qty int32) (map[string]any, error) {
	payload := map[string]any{
		"sku_id":   strings.TrimSpace(skuID),
		"quantity": qty,
	}
	body, err := c.doJSON(ctx, http.MethodPost, "/api/v1/cart/items", rawBearer, payload, false)
	if err != nil {
		return nil, err
	}
	return decodeMap(body)
}

// ListSellerOrders calls GET /api/v1/seller/orders using the seller's JWT.
func (c *HTTPGatewayClient) ListSellerOrders(ctx context.Context, rawBearer string) ([]map[string]any, error) {
	body, err := c.doJSON(ctx, http.MethodGet, "/api/v1/seller/orders?page=1&page_size=100", rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	return decodeItemsList(body)
}

// GetSellerBalance calls GET /api/v1/seller/balance using the seller's JWT.
func (c *HTTPGatewayClient) GetSellerBalance(ctx context.Context, rawBearer string) (map[string]any, error) {
	body, err := c.doJSON(ctx, http.MethodGet, "/api/v1/seller/balance", rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	return decodeMap(body)
}

// GetSellerReport calls GET /api/v1/seller/reports (backed by OrderService.GetSellerReport)
// using strictly the seller's own JWT.
func (c *HTTPGatewayClient) GetSellerReport(ctx context.Context, rawBearer, fromDate, toDate string, topLimit int32) (map[string]any, error) {
	q := url.Values{}
	if fromDate != "" {
		q.Set("from_date", fromDate)
	}
	if toDate != "" {
		q.Set("to_date", toDate)
	}
	if topLimit > 0 {
		q.Set("top_limit", fmt.Sprintf("%d", topLimit))
	}
	path := "/api/v1/seller/reports"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	body, err := c.doJSON(ctx, http.MethodGet, path, rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	obj, err := decodeMap(body)
	if err != nil {
		return nil, err
	}
	if inner, ok := obj["report"].(map[string]any); ok {
		return inner, nil
	}
	return obj, nil
}

// ListSellerProducts calls GET /api/v1/seller/products using the seller's JWT.
func (c *HTTPGatewayClient) ListSellerProducts(ctx context.Context, rawBearer string) ([]map[string]any, error) {
	body, err := c.doJSON(ctx, http.MethodGet, "/api/v1/seller/products?page=1&page_size=100", rawBearer, nil, true)
	if err != nil {
		return nil, err
	}
	return decodeItemsList(body)
}

// CreateChatConversation calls POST /api/v1/chat/conversations using the buyer's JWT. Never retries POST.
func (c *HTTPGatewayClient) CreateChatConversation(ctx context.Context, rawBearer, shopID string) (map[string]any, error) {
	return c.CreateChatConversationWithMessage(ctx, rawBearer, shopID, "")
}

// CreateChatConversationWithMessage calls POST /api/v1/chat/conversations with an optional initial_message
// (for AI handoff summary) using strictly the buyer's JWT. Never retries POST.
func (c *HTTPGatewayClient) CreateChatConversationWithMessage(ctx context.Context, rawBearer, shopID, initialMessage string) (map[string]any, error) {
	payload := map[string]any{
		"shop_id": strings.TrimSpace(shopID),
	}
	if msg := strings.TrimSpace(initialMessage); msg != "" {
		payload["initial_message"] = msg
	}
	body, err := c.doJSON(ctx, http.MethodPost, "/api/v1/chat/conversations", rawBearer, payload, false)
	if err != nil {
		return nil, err
	}
	return decodeMap(body)
}

func (c *HTTPGatewayClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	rawBearer string,
	reqBody any,
	retryable bool,
) ([]byte, error) {
	token := sanitizeBearerToken(rawBearer)
	if token == "" {
		return nil, ErrMissingBearerToken
	}

	var encoded []byte
	if reqBody != nil {
		var err error
		encoded, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	attempts := 1
	if retryable && method == http.MethodGet {
		attempts = 3
	}

	endpoint := c.baseURL + path
	return resilience.CallWithRetry(
		ctx,
		c.cb,
		attempts,
		100*time.Millisecond,
		c.timeout,
		resilience.IsTransientHTTPError,
		func(attemptCtx context.Context) ([]byte, error) {
			var bodyReader io.Reader
			if len(encoded) > 0 {
				bodyReader = bytes.NewReader(encoded)
			}
			req, err := http.NewRequestWithContext(attemptCtx, method, endpoint, bodyReader)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/json")
			if len(encoded) > 0 {
				req.Header.Set("Content-Type", "application/json")
			}
			otel.GetTextMapPropagator().Inject(attemptCtx, propagation.HeaderCarrier(req.Header))

			resp, err := c.client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			if err != nil {
				return nil, err
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				snippet := string(respBytes)
				if len(snippet) > 512 {
					snippet = snippet[:512]
				}
				return nil, &resilience.HTTPStatusError{
					StatusCode: resp.StatusCode,
					Body:       snippet,
				}
			}
			return respBytes, nil
		},
	)
}

func sanitizeBearerToken(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 7 && strings.EqualFold(s[:7], "Bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	return s
}

func decodeMap(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode gateway json object: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func decodeItemsList(data []byte) ([]map[string]any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []map[string]any{}, nil
	}
	if trimmed[0] == '[' {
		var list []map[string]any
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("decode gateway list: %w", err)
		}
		if list == nil {
			list = []map[string]any{}
		}
		return list, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("decode gateway envelope: %w", err)
	}
	for _, key := range []string{"items", "orders", "products"} {
		if raw, ok := envelope[key]; ok {
			var list []map[string]any
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, fmt.Errorf("decode gateway %s: %w", key, err)
			}
			if list == nil {
				list = []map[string]any{}
			}
			return list, nil
		}
	}
	return []map[string]any{}, nil
}
