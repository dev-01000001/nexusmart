package tools

import (
	"context"

	"github.com/nexusmart/core/services/assistant/internal/llm"
)

// CallContext carries the verified end-user identity and raw JWT forwarded verbatim
// to Gateway APIs. Tools MUST NEVER escalate privileges or use a service token.
type CallContext struct {
	UserID       string
	UserRole     string // "", "buyer", "seller", "admin", "super_admin"
	SellerShopID string // From verified JWT claims only (never request body)
	ShopID       string // Storefront shop_id scoping product retrieval & handoff
	RawBearer    string // End-user Authorization Bearer token (empty if anonymous)
	Confirmed    bool   // Explicit user confirmation for mutating tools (e.g. add_to_cart)
}

// Result is returned by executing a tool.
type Result struct {
	Name                 string         `json:"name"`
	OK                   bool           `json:"ok"`
	RequiresAuth         bool           `json:"requires_auth,omitempty"`
	RequiresConfirmation bool           `json:"requires_confirmation,omitempty"`
	Error                string         `json:"error,omitempty"`
	Data                 map[string]any `json:"data,omitempty"`
	Private              bool           `json:"-"` // True if result contains user-private data (disables shared cache)
}

// Executor exposes role-filtered tool schemas and executes tool calls using CallContext.
type Executor interface {
	AllowedTools(role string) []llm.ToolDefinition
	Execute(ctx context.Context, cc CallContext, name string, args map[string]any) Result
}
