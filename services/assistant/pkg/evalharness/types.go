package evalharness

import (
	"context"
	"net/http"
)

// SKU mirrors a retrievable product SKU for evaluation fixtures.
type SKU struct {
	ID             string            `json:"id"`
	SKUCode        string            `json:"sku_code,omitempty"`
	Name           string            `json:"name"`
	PriceVND       int64             `json:"price_vnd"`
	AvailableStock int32             `json:"available_stock"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

// Product mirrors a retrievable catalog product for evaluation fixtures.
type Product struct {
	ID          string `json:"id"`
	ShopID      string `json:"shop_id"`
	ShopName    string `json:"shop_name,omitempty"`
	CategoryID  string `json:"category_id,omitempty"`
	Category    string `json:"category,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MinPriceVND int64  `json:"min_price_vnd"`
	MaxPriceVND int64  `json:"max_price_vnd"`
	ImageURL    string `json:"image_url,omitempty"`
	InStock     bool   `json:"in_stock"`
	SKUs        []SKU  `json:"skus,omitempty"`
}

// GroundedProduct identifies a product returned by tool calls during a turn.
type GroundedProduct struct {
	ID     string
	ShopID string
	Name   string
}

// MockGateway provides deterministic Gateway responses to assistant tools during evals.
type MockGateway interface {
	ListBuyerOrders(ctx context.Context, rawBearer, storefrontShopID, status string) ([]map[string]any, error)
	GetOrder(ctx context.Context, rawBearer, orderID string) (map[string]any, error)
	AddCartItem(ctx context.Context, rawBearer, skuID string, qty int32) (map[string]any, error)
	ListSellerOrders(ctx context.Context, rawBearer string) ([]map[string]any, error)
	GetSellerBalance(ctx context.Context, rawBearer string) (map[string]any, error)
	ListSellerProducts(ctx context.Context, rawBearer string) ([]map[string]any, error)
	CreateChatConversation(ctx context.Context, rawBearer, shopID string) (map[string]any, error)
}

// ChatHistoryMessage represents a prior message in a buyer-seller chat thread.
type ChatHistoryMessage struct {
	Sender string `json:"sender"`
	Body   string `json:"body"`
}

// SuggestReplyReq carries seller reply suggestion input for chat-injection evals.
type SuggestReplyReq struct {
	ConversationID  string               `json:"conversation_id"`
	ProductID       string               `json:"product_id"`
	CustomerMessage string               `json:"customer_message"`
	History         []ChatHistoryMessage `json:"history"`
}

// ReplySuggestion is a single draft reply suggestion returned to the seller.
type ReplySuggestion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Tone string `json:"tone"`
}

// SuggestReplyResp summarizes the result of SuggestReply.
type SuggestReplyResp struct {
	Suggestions       []ReplySuggestion `json:"suggestions"`
	NeedsMoreInfo     bool              `json:"needs_more_info"`
	InjectionDetected bool              `json:"injection_detected"`
	AIGenerated       bool              `json:"ai_generated"`
}

// StackConfig configures an in-process assistant stack for quantitative evals.
type StackConfig struct {
	Mode                string // "mock" or "anthropic"
	Model               string // default "claude-opus-5-5"
	AnthropicAPIKey     string
	AnthropicBaseURL    string
	JWTSecret           string
	Products            []Product
	Gateway             MockGateway
	DailyUserTokenLimit int64
	DailyCostLimitVND   int64
	AbuseStrikeLimit    int
}

// Stack exposes the real in-process assistant SSE HTTP handler, SuggestReply,
// JWT token signing, and grounding verification for evals/assistant.
type Stack struct {
	Handler http.Handler

	signTokenFn       func(userID, role, shopID string) (string, error)
	suggestReplyFn    func(ctx context.Context, userID, shopID, rawBearer string, req SuggestReplyReq) (*SuggestReplyResp, error)
	verifyGroundingFn func(text, allowedShopID string, retrieved []GroundedProduct) (bool, []string)
}

// SignToken mints an HS256 end-user access token valid for Stack's auth manager.
func (s *Stack) SignToken(userID, role, shopID string) (string, error) {
	return s.signTokenFn(userID, role, shopID)
}

// SuggestReply runs the real service.SuggestReply pipeline for seller chat-injection evals.
func (s *Stack) SuggestReply(ctx context.Context, userID, shopID, rawBearer string, req SuggestReplyReq) (*SuggestReplyResp, error) {
	return s.suggestReplyFn(ctx, userID, shopID, rawBearer, req)
}

// VerifyGrounding checks output text against retrieved products and allowed shop_id.
func (s *Stack) VerifyGrounding(text, allowedShopID string, retrieved []GroundedProduct) (bool, []string) {
	return s.verifyGroundingFn(text, allowedShopID, retrieved)
}
