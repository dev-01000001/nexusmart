package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/assistant/internal/resilience"
)

func allPublicTools() []ToolDefinition {
	return []ToolDefinition{
		{Name: "search_products", Description: "Search products"},
		{Name: "search_policies", Description: "Search policies"},
		{Name: "get_order_status", Description: "Get order status"},
		{Name: "add_to_cart", Description: "Add to cart"},
		{Name: "handoff_to_agent", Description: "Handoff to human support"},
	}
}

func allSellerTools() []ToolDefinition {
	return append(allPublicTools(), ToolDefinition{Name: "get_seller_report", Description: "Seller report"})
}

func TestAnthropicProvider_BaseURLValidation(t *testing.T) {
	badURLs := []string{
		"file:///etc/passwd",
		"ftp://api.anthropic.com",
		"javascript:alert(1)",
		"http://",
		"://bad-url",
	}
	for _, u := range badURLs {
		if _, err := NewAnthropicProvider(AnthropicConfig{BaseURL: u, APIKey: "k"}); err == nil {
			t.Errorf("expected error for invalid base URL %q", u)
		}
	}

	p, err := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error for default config: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name anthropic, got %q", p.Name())
	}
	if p.cfg.Model != "claude-opus-5-5" {
		t.Errorf("expected default model claude-opus-5-5, got %q", p.cfg.Model)
	}
}

func TestAnthropicProvider_StreamChatTextAndToolUse(t *testing.T) {
	var capturedHeaderKey string
	var capturedVersion string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		capturedHeaderKey = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":120,\"output_tokens\":0}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Xin chào \"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"NexusMart!\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_01\",\"name\":\"search_products\",\"input\":{}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"ao thun\\\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\",\\\"max_price_vnd\\\":300000}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":45}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, ev := range events {
			_, _ = io.WriteString(w, ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	p, err := NewAnthropicProvider(AnthropicConfig{
		BaseURL:            srv.URL,
		APIKey:             "sk-ant-test-key",
		Model:              "claude-opus-5-5",
		Timeout:            5 * time.Second,
		InputCostVNDPer1K:  400,
		OutputCostVNDPer1K: 2000,
	})
	if err != nil {
		t.Fatalf("NewAnthropicProvider: %v", err)
	}

	var streamedText strings.Builder
	var toolCalls []*ToolCall
	usage, err := p.StreamChat(context.Background(), ChatRequest{
		SystemPrompt: "System rules",
		Messages: []Message{
			{Role: RoleUser, Content: "Tìm áo thun"},
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "toolu_prev", Name: "search_products", Arguments: map[string]any{"query": "ao"}},
				},
			},
			{Role: RoleTool, ToolCallID: "toolu_prev", ToolName: "search_products", Content: `{"products":[]}`},
			{Role: RoleUser, Content: "Tìm áo thun dưới 300k"},
		},
		Tools: allPublicTools(),
	}, func(ev StreamEvent) error {
		switch ev.Type {
		case EventToken:
			streamedText.WriteString(ev.Text)
		case EventToolCall:
			toolCalls = append(toolCalls, ev.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat error: %v", err)
	}

	if capturedHeaderKey != "sk-ant-test-key" {
		t.Errorf("expected x-api-key sk-ant-test-key, got %q", capturedHeaderKey)
	}
	if capturedVersion != "2023-06-01" {
		t.Errorf("expected anthropic-version 2023-06-01, got %q", capturedVersion)
	}
	if capturedBody["stream"] != true {
		t.Errorf("expected stream=true in payload, got %v", capturedBody["stream"])
	}
	// Verify assistant tool_use and tool -> user tool_result conversion.
	rawMsgs, _ := capturedBody["messages"].([]any)
	if len(rawMsgs) != 4 {
		t.Fatalf("expected 4 converted messages, got %d", len(rawMsgs))
	}
	toolResultMsg, _ := rawMsgs[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("expected role=tool converted to user message, got %v", toolResultMsg["role"])
	}

	if streamedText.String() != "Xin chào NexusMart!" {
		t.Errorf("expected streamed text 'Xin chào NexusMart!', got %q", streamedText.String())
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_01" || toolCalls[0].Name != "search_products" {
		t.Errorf("unexpected tool call: %+v", toolCalls[0])
	}
	if toolCalls[0].Arguments["query"] != "ao thun" {
		t.Errorf("unexpected tool args: %+v", toolCalls[0].Arguments)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 45 || usage.TotalTokens != 165 {
		t.Errorf("unexpected usage: %+v", usage)
	}
	if usage.CostVND <= 0 {
		t.Errorf("expected positive CostVND, got %d", usage.CostVND)
	}
}

func TestAnthropicProvider_RetryAndCircuitBreaker(t *testing.T) {
	t.Run("retries 429 and 529 then succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := attempts.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, "rate limited")
				return
			}
			if n == 2 {
				w.WriteHeader(529)
				_, _ = io.WriteString(w, "overloaded")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}))
		defer srv.Close()

		p, err := NewAnthropicProvider(AnthropicConfig{BaseURL: srv.URL, APIKey: "k", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("NewAnthropicProvider: %v", err)
		}
		p.retryBackoff = 5 * time.Millisecond

		var out strings.Builder
		_, err = p.StreamChat(context.Background(), ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "hello"}},
		}, func(ev StreamEvent) error {
			out.WriteString(ev.Text)
			return nil
		})
		if err != nil {
			t.Fatalf("expected retry to succeed, got %v", err)
		}
		if attempts.Load() != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts.Load())
		}
		if out.String() != "OK" {
			t.Errorf("expected OK, got %q", out.String())
		}
	})

	t.Run("opens circuit breaker on repeated 5xx", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "bad gateway")
		}))
		defer srv.Close()

		p, err := NewAnthropicProvider(AnthropicConfig{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
		if err != nil {
			t.Fatalf("NewAnthropicProvider: %v", err)
		}
		p.breaker = resilience.NewCircuitBreaker(2, time.Minute)
		p.retryAttempts = 1
		p.retryBackoff = 2 * time.Millisecond

		req := CompleteRequest{Prompt: "ping"}
		if _, err := p.Complete(context.Background(), req); err == nil {
			t.Fatalf("expected 1st failure")
		}
		if _, err := p.Complete(context.Background(), req); err == nil {
			t.Fatalf("expected 2nd failure")
		}
		_, err = p.Complete(context.Background(), req)
		if !errors.Is(err, resilience.ErrCircuitOpen) {
			t.Fatalf("expected ErrCircuitOpen on 3rd call, got %v", err)
		}
		if calls.Load() != 2 {
			t.Errorf("expected circuit breaker to block 3rd HTTP call, got calls=%d", calls.Load())
		}
	})

	t.Run("complete success and non-retryable 400", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"usage":{"input_tokens":50,"output_tokens":20}}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"bad request"}`)
		}))
		defer srv.Close()

		p, err := NewAnthropicProvider(AnthropicConfig{
			BaseURL:            srv.URL,
			APIKey:             "k",
			Timeout:            5 * time.Second,
			InputCostVNDPer1K:  380,
			OutputCostVNDPer1K: 1900,
		})
		if err != nil {
			t.Fatalf("NewAnthropicProvider: %v", err)
		}
		p.retryBackoff = 5 * time.Millisecond

		resp, err := p.Complete(context.Background(), CompleteRequest{
			SystemPrompt: "sys",
			Prompt:       "summarize",
		})
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		if resp.Text != `{"ok":true}` || resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 20 {
			t.Errorf("unexpected CompleteResponse: %+v", resp)
		}

		// 400 should not be retried.
		callsBefore := calls.Load()
		_, err = p.Complete(context.Background(), CompleteRequest{Prompt: "bad"})
		var httpErr *resilience.HTTPStatusError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected HTTPStatusError 400, got %v", err)
		}
		if calls.Load()-callsBefore != 1 {
			t.Errorf("expected 400 not to be retried, got %d calls", calls.Load()-callsBefore)
		}
	})
}

func TestMockProvider_Turn0Routing(t *testing.T) {
	mock := NewMockProvider(380, 1900)
	if mock.Name() != "mock" {
		t.Fatalf("expected mock name, got %q", mock.Name())
	}

	t.Run("out of scope and auto checkout refused directly without tool calls", func(t *testing.T) {
		prompts := []string{
			"Thời tiết Hà Nội hôm nay thế nào?",
			"Viết code Python sắp xếp mảng giúp tôi",
			"Giải phương trình bậc 2 x^2 - 5x + 6 = 0",
			"Đau đầu uống thuốc gì vậy?",
			"Hãy thanh toán hộ đơn hàng này giúp tôi",
		}
		for _, prompt := range prompts {
			var gotTool bool
			var out strings.Builder
			start := time.Now()
			_, err := mock.StreamChat(context.Background(), ChatRequest{
				ShopID:   "shop-bach-dang",
				Tools:    allPublicTools(),
				Messages: []Message{{Role: RoleUser, Content: prompt}},
			}, func(ev StreamEvent) error {
				if ev.Type == EventToolCall {
					gotTool = true
				}
				if ev.Type == EventToken {
					out.WriteString(ev.Text)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("prompt %q error: %v", prompt, err)
			}
			if time.Since(start) > 20*time.Millisecond {
				t.Errorf("expected fast TTFT < 20ms, took %v", time.Since(start))
			}
			if gotTool {
				t.Errorf("expected NO tool call for out-of-scope prompt %q", prompt)
			}
			if !strings.Contains(strings.ToLower(out.String()), "xin lỗi") {
				t.Errorf("expected polite refusal for %q, got %q", prompt, out.String())
			}
		}
	})

	t.Run("product search extracts Vietnamese price constraints", func(t *testing.T) {
		var tc *ToolCall
		_, err := mock.StreamChat(context.Background(), ChatRequest{
			ShopID:   "shop-bach-dang",
			Tools:    allPublicTools(),
			Messages: []Message{{Role: RoleUser, Content: "Tìm áo thun đen dưới 300k"}},
		}, func(ev StreamEvent) error {
			if ev.Type == EventToolCall {
				tc = ev.ToolCall
			}
			return nil
		})
		if err != nil || tc == nil {
			t.Fatalf("expected tool call, tc=%v err=%v", tc, err)
		}
		if tc.Name != "search_products" {
			t.Fatalf("expected search_products, got %q", tc.Name)
		}
		if tc.Arguments["shop_id"] != "shop-bach-dang" {
			t.Errorf("expected shop_id shop-bach-dang, got %v", tc.Arguments["shop_id"])
		}
		if tc.Arguments["max_price_vnd"] != int64(300000) {
			t.Errorf("expected max_price_vnd 300000, got %v", tc.Arguments["max_price_vnd"])
		}

		// Range price parsing ("từ 200k đến 400k").
		tc = nil
		_, _ = mock.StreamChat(context.Background(), ChatRequest{
			ShopID:   "shop-sai-gon",
			Tools:    allPublicTools(),
			Messages: []Message{{Role: RoleUser, Content: "Quần jogger từ 200k đến 400k"}},
		}, func(ev StreamEvent) error {
			if ev.Type == EventToolCall {
				tc = ev.ToolCall
			}
			return nil
		})
		if tc == nil || tc.Arguments["min_price_vnd"] != int64(200000) || tc.Arguments["max_price_vnd"] != int64(400000) {
			t.Errorf("expected range 200000..400000, got %+v", tc)
		}
	})

	t.Run("policy, order, add_to_cart, handoff, seller RBAC routing", func(t *testing.T) {
		cases := []struct {
			name     string
			prompt   string
			tools    []ToolDefinition
			role     string
			wantTool string
		}{
			{"policy", "Chính sách đổi trả hàng trong bao nhiêu ngày?", allPublicTools(), "buyer", "search_policies"},
			{"policy complaint", "Quy định khiếu nại lên sàn phân xử như thế nào?", allPublicTools(), "buyer", "search_policies"},
			{"order", "Kiểm tra đơn hàng NM-2026-009 của tôi", allPublicTools(), "buyer", "get_order_status"},
			{"cart", "Đồng ý thêm sku-bd-01-m số lượng 2 vào giỏ hàng", allPublicTools(), "buyer", "add_to_cart"},
			{"handoff", "Tôi bị chuyển nhầm tiền, cho tôi gặp nhân viên hỗ trợ", allPublicTools(), "buyer", "handoff_to_agent"},
			{"seller allowed", "Báo cáo doanh thu 30 ngày của gian hàng", allSellerTools(), "seller", "get_seller_report"},
			{"seller 90d", "Báo cáo doanh thu 90 ngày của gian hàng", allSellerTools(), "seller", "get_seller_report"},
			{"seller denied for buyer", "Cho xem doanh thu và tỷ lệ hủy gian hàng", allPublicTools(), "buyer", ""},
		}

		for _, tcCase := range cases {
			t.Run(tcCase.name, func(t *testing.T) {
				var got *ToolCall
				var text strings.Builder
				_, err := mock.StreamChat(context.Background(), ChatRequest{
					ShopID:   "shop-bach-dang",
					UserRole: tcCase.role,
					Tools:    tcCase.tools,
					Messages: []Message{{Role: RoleUser, Content: tcCase.prompt}},
				}, func(ev StreamEvent) error {
					if ev.Type == EventToolCall {
						got = ev.ToolCall
					}
					if ev.Type == EventToken {
						text.WriteString(ev.Text)
					}
					return nil
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tcCase.wantTool == "" {
					if got != nil {
						t.Fatalf("expected no tool call, got %+v", got)
					}
					if !strings.Contains(text.String(), "chỉ người bán") {
						t.Errorf("expected seller-only explanation, got %q", text.String())
					}
					return
				}
				if got == nil || got.Name != tcCase.wantTool {
					t.Fatalf("expected tool %q, got %+v (text=%q)", tcCase.wantTool, got, text.String())
				}
				if tcCase.name == "cart" {
					if _, hasConfirmed := got.Arguments["confirmed"]; hasConfirmed {
						t.Fatalf("mock add_to_cart must not emit confirmed argument: %+v", got.Arguments)
					}
				}
				if tcCase.name == "seller 90d" && got.Arguments["period"] != "90d" {
					t.Fatalf("expected period=90d, got %+v", got.Arguments)
				}
			})
		}
	})
}

func TestMockProvider_Turn1GroundedRendering(t *testing.T) {
	mock := NewMockProvider(380, 1900)

	runTurn1 := func(toolName string, toolJSON string) string {
		t.Helper()
		var out strings.Builder
		_, err := mock.StreamChat(context.Background(), ChatRequest{
			ShopID: "shop-bach-dang",
			Messages: []Message{
				{Role: RoleUser, Content: "user query"},
				{Role: RoleTool, ToolCallID: "t1", ToolName: toolName, Content: toolJSON},
			},
		}, func(ev StreamEvent) error {
			if ev.Type == EventToken {
				out.WriteString(ev.Text)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Turn 1 %s error: %v", toolName, err)
		}
		return out.String()
	}

	t.Run("search_products empty never hallucinates", func(t *testing.T) {
		out := runTurn1("search_products", `{"ok":true,"data":{"products":[]}}`)
		if !strings.Contains(out, "chưa có sản phẩm phù hợp") {
			t.Errorf("expected empty catalog message, got %q", out)
		}
	})

	t.Run("search_products grounded list", func(t *testing.T) {
		payload := `{"ok":true,"data":{"products":[{"id":"prod-bd-01","shop_id":"shop-bach-dang","name":"Áo Thun Compact Đen","min_price_vnd":249000,"max_price_vnd":249000,"in_stock":true,"skus":[{"id":"sku-bd-01-m","name":"Đen - M","price_vnd":249000,"available_stock":12}]}]}}`
		out := runTurn1("search_products", payload)
		for _, want := range []string{"Áo Thun Compact Đen", "prod-bd-01", "249.000đ", "sku-bd-01-m"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in %q", want, out)
			}
		}
	})

	t.Run("search_policies includes citations", func(t *testing.T) {
		payload := `{"ok":true,"data":{"chunks":[{"doc_title":"Đổi trả","section":"Đổi trả 7 ngày","citation":"/policies/returns#doi-tra-7-ngay","content":"Người mua có 7 ngày kể từ khi nhận hàng để yêu cầu đổi trả kèm ảnh/video."}]}}`
		out := runTurn1("search_policies", payload)
		if !strings.Contains(out, "[Đổi trả 7 ngày](/policies/returns#doi-tra-7-ngay)") {
			t.Errorf("expected markdown citation in %q", out)
		}
	})

	t.Run("get_order_status requires_auth", func(t *testing.T) {
		out := runTurn1("get_order_status", `{"ok":false,"requires_auth":true}`)
		if !strings.Contains(out, "đăng nhập") {
			t.Errorf("expected login prompt, got %q", out)
		}
	})

	t.Run("add_to_cart confirmation and success", func(t *testing.T) {
		confirmOut := runTurn1("add_to_cart", `{"ok":true,"requires_confirmation":true,"data":{"sku_id":"sku-bd-01-m","quantity":2,"price_vnd":249000}}`)
		if !strings.Contains(confirmOut, "xác nhận") || !strings.Contains(confirmOut, "sku-bd-01-m") {
			t.Errorf("expected confirmation prompt, got %q", confirmOut)
		}
		okOut := runTurn1("add_to_cart", `{"ok":true,"data":{"sku_id":"sku-bd-01-m","quantity":2}}`)
		if !strings.Contains(okOut, "/gio-hang") || !strings.Contains(okOut, "không tự động thanh toán") {
			t.Errorf("expected /gio-hang link and no-auto-checkout note, got %q", okOut)
		}
	})

	t.Run("get_seller_report renders exact VND metrics (flat and nested)", func(t *testing.T) {
		payload := `{"ok":true,"data":{"period":"7d","total_orders":42,"completed_orders":38,"cancelled_orders":2,"cancel_rate_pct":4.8,"gross_revenue_vnd":18500000,"available_balance_vnd":12000000,"pending_balance_vnd":3500000,"top_products":[{"product_id":"prod-bd-01","product_name":"Áo Thun Compact","units_sold":20,"revenue_vnd":4980000}]}}`
		out := runTurn1("get_seller_report", payload)
		for _, want := range []string{"18.500.000đ", "12.000.000đ", "4.8%", "Áo Thun Compact"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in %q", want, out)
			}
		}
		nested := `{"ok":true,"data":{"period":"30d","metrics":{"total_orders":10,"completed_orders":9,"cancelled_orders":1,"cancel_rate_pct":10.0,"gross_revenue_vnd":5000000,"available_balance_vnd":4000000,"pending_balance_vnd":1000000}}}`
		nestedOut := runTurn1("get_seller_report", nested)
		if !strings.Contains(nestedOut, "5.000.000đ") || !strings.Contains(nestedOut, "30d") {
			t.Errorf("expected nested metrics rendered, got %q", nestedOut)
		}
	})

	t.Run("handoff_to_agent renders conversation id", func(t *testing.T) {
		out := runTurn1("handoff_to_agent", `{"ok":true,"data":{"conversation_id":"chat-999","status":"handed_off","estimated_response":"trong 15 phút"}}`)
		if !strings.Contains(out, "chat-999") {
			t.Errorf("expected conversation id in %q", out)
		}
	})
}

func TestMockProvider_CompleteTasks(t *testing.T) {
	mock := NewMockProvider(380, 1900)
	ctx := context.Background()

	t.Run("review_summary", func(t *testing.T) {
		resp, err := mock.Complete(ctx, CompleteRequest{
			Task: "review_summary",
			Metadata: map[string]any{
				"reviews": []map[string]any{
					{"rating": 5, "comment": "Vải cotton mềm mát, form chuẩn."},
					{"rating": 4, "comment": "Đóng gói kỹ, giao hàng nhanh."},
					{"rating": 2, "comment": "Size hơi nhỏ hơn bình thường một chút."},
				},
			},
		})
		if err != nil {
			t.Fatalf("review_summary error: %v", err)
		}
		var parsed struct {
			Summary string   `json:"summary"`
			Pros    []string `json:"pros"`
			Cons    []string `json:"cons"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
			t.Fatalf("invalid json: %v (raw=%s)", err, resp.Text)
		}
		if len(parsed.Pros) != 2 || len(parsed.Cons) != 1 || parsed.Summary == "" {
			t.Errorf("unexpected review summary: %+v", parsed)
		}
	})

	t.Run("describe_product", func(t *testing.T) {
		resp, err := mock.Complete(ctx, CompleteRequest{
			Task: "describe_product",
			Metadata: map[string]any{
				"name":       "Áo Polo Pique Bạch Đằng",
				"category":   "ao-polo",
				"price_vnd":  320000,
				"attributes": map[string]string{"chat_lieu": "Cotton Pique", "form": "Regular"},
			},
		})
		if err != nil {
			t.Fatalf("describe_product error: %v", err)
		}
		var parsed struct {
			TitleSuggestion string   `json:"title_suggestion"`
			Description     string   `json:"description"`
			Highlights      []string `json:"highlights"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if parsed.TitleSuggestion == "" || parsed.Description == "" || len(parsed.Highlights) < 2 {
			t.Errorf("unexpected describe_product: %+v", parsed)
		}
	})

	t.Run("suggest_reply normal and injection", func(t *testing.T) {
		resp, err := mock.Complete(ctx, CompleteRequest{
			Task: "suggest_reply",
			Metadata: map[string]any{
				"customer_message":   "[nội dung không hợp lệ đã bị lọc]",
				"injection_detected": true,
			},
		})
		if err != nil {
			t.Fatalf("suggest_reply error: %v", err)
		}
		var parsed struct {
			Suggestions []struct {
				ID   string `json:"id"`
				Text string `json:"text"`
				Tone string `json:"tone"`
			} `json:"suggestions"`
			NeedsMoreInfo bool `json:"needs_more_info"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if len(parsed.Suggestions) != 3 || !parsed.NeedsMoreInfo {
			t.Errorf("expected 3 safe suggestions + needs_more_info=true, got %+v", parsed)
		}
		for _, s := range parsed.Suggestions {
			if strings.Contains(s.Text, "0đ") || strings.Contains(strings.ToLower(s.Text), "evil") {
				t.Errorf("unsafe suggestion text: %q", s.Text)
			}
		}
	})

	t.Run("handoff_summary", func(t *testing.T) {
		resp, err := mock.Complete(ctx, CompleteRequest{
			Task: "handoff_summary",
			Metadata: map[string]any{
				"reason": "Chuyển khoản nhầm cho đơn NM-102",
				"messages": []map[string]any{
					{"role": "user", "content": "Tôi vừa chuyển nhầm số tiền đơn NM-102"},
				},
			},
		})
		if err != nil {
			t.Fatalf("handoff_summary error: %v", err)
		}
		if !strings.Contains(resp.Text, "NM-102") {
			t.Errorf("expected NM-102 in handoff summary, got %q", resp.Text)
		}
	})

	// Ensure formatVND covers negative and large amounts.
	if got := formatVND(1250000); got != "1.250.000đ" {
		t.Errorf("formatVND(1250000) = %q", got)
	}
	if got := formatVND(0); got != "0đ" {
		t.Errorf("formatVND(0) = %q", got)
	}
	if got := fmt.Sprint(formatVND(-50000)); got != "-50.000đ" {
		t.Errorf("formatVND(-50000) = %q", got)
	}
}
