package llm

import "context"

// Role constants for LLM conversation messages.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message represents a single turn in a conversation, including optional tool calls/results.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	IsError    bool       `json:"is_error,omitempty"`
}

// ToolDefinition describes a callable function exposed to the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolCall represents a tool invocation emitted by the LLM.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ChatRequest is sent to Provider.StreamChat.
type ChatRequest struct {
	Model        string
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDefinition
	MaxTokens    int
	Temperature  float64
	ShopID       string
	UserRole     string
}

// EventType enumerates streaming event types from the LLM.
type EventType string

const (
	EventToken    EventType = "token"
	EventToolCall EventType = "tool_call"
)

// StreamEvent is emitted incrementally while streaming a model turn.
type StreamEvent struct {
	Type     EventType
	Text     string
	ToolCall *ToolCall
}

// Usage tracks token consumption and integer VND cost for an LLM call.
type Usage struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	TotalTokens  int   `json:"total_tokens"`
	CostVND      int64 `json:"cost_vnd"`
}

// Add accumulates token counts and VND cost from other into u.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.TotalTokens += other.TotalTokens
	u.CostVND += other.CostVND
}

// CompleteRequest is used for single-shot structured generation tasks
// (review summary, product description, reply suggestion, handoff summary).
type CompleteRequest struct {
	Task         string // "review_summary" | "describe_product" | "suggest_reply" | "handoff_summary"
	Model        string
	SystemPrompt string
	Prompt       string
	Metadata     map[string]any
	MaxTokens    int
	Temperature  float64
}

// CompleteResponse is returned by Provider.Complete.
type CompleteResponse struct {
	Text  string
	Usage Usage
}

// Provider abstracts LLM backends (`mock` or `anthropic`).
type Provider interface {
	Name() string
	StreamChat(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) (Usage, error)
	Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error)
}

// EstimateCostVND computes integer VND cost from input/output token counts.
func EstimateCostVND(inputTokens, outputTokens int, inputVNDPer1K, outputVNDPer1K int64) int64 {
	if inputTokens <= 0 && outputTokens <= 0 {
		return 0
	}
	inCost := (int64(inputTokens)*inputVNDPer1K + 999) / 1000
	outCost := (int64(outputTokens)*outputVNDPer1K + 999) / 1000
	total := inCost + outCost
	if total <= 0 {
		total = 1
	}
	return total
}

// EstimateTokens approximates token count for Vietnamese/English UTF-8 text.
func EstimateTokens(text string) int {
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	tokens := (n + 2) / 3
	if tokens < 1 {
		return 1
	}
	return tokens
}
