package llm

import (
	"bufio"
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
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-opus-5-5"
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 1024
	maxSSEScannerBufBytes   = 1024 * 1024 // 1 MiB
	maxErrorBodyBytes       = 4096
)

// Compile-time check that AnthropicProvider implements Provider.
var _ Provider = (*AnthropicProvider)(nil)

// AnthropicConfig configures AnthropicProvider.
type AnthropicConfig struct {
	BaseURL            string
	APIKey             string
	Model              string
	Timeout            time.Duration
	InputCostVNDPer1K  int64
	OutputCostVNDPer1K int64
}

// AnthropicProvider calls the Anthropic Messages API using Go stdlib net/http
// with SSE streaming, retries with exponential backoff, and a circuit breaker.
type AnthropicProvider struct {
	cfg           AnthropicConfig
	client        *http.Client
	breaker       *resilience.CircuitBreaker
	retryAttempts int
	retryBackoff  time.Duration
}

// NewAnthropicProvider validates configuration (including SSRF scheme/host checks)
// and constructs an AnthropicProvider.
func NewAnthropicProvider(cfg AnthropicConfig) (*AnthropicProvider, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid anthropic base url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("invalid anthropic base url: scheme must be http or https with non-empty host")
	}
	cfg.BaseURL = strings.TrimRight(base, "/")

	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultAnthropicModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.InputCostVNDPer1K < 0 {
		cfg.InputCostVNDPer1K = 0
	}
	if cfg.OutputCostVNDPer1K < 0 {
		cfg.OutputCostVNDPer1K = 0
	}

	return &AnthropicProvider{
		cfg: cfg,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:          32,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: cfg.Timeout,
			},
		},
		breaker:       resilience.NewCircuitBreaker(5, 15*time.Second),
		retryAttempts: 3,
		retryBackoff:  150 * time.Millisecond,
	}, nil
}

// Name returns the provider identifier.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Stream      bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicCompleteResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type streamResponseHandle struct {
	resp   *http.Response
	cancel context.CancelFunc
}

func (h *streamResponseHandle) close() {
	if h == nil {
		return
	}
	if h.resp != nil && h.resp.Body != nil {
		_ = h.resp.Body.Close()
	}
	if h.cancel != nil {
		h.cancel()
	}
}

// StreamChat invokes POST <BaseURL>/v1/messages with stream=true, retrying transient
// errors before the stream begins, and emits text/tool-call SSE events via onEvent.
func (p *AnthropicProvider) StreamChat(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) (Usage, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return Usage{}, errors.New("anthropic api key is required")
	}

	payload, err := p.buildChatPayload(req, true)
	if err != nil {
		return Usage{}, err
	}

	handle, err := p.openStreamWithRetry(ctx, payload)
	if err != nil {
		return Usage{}, err
	}
	defer handle.close()

	return p.consumeSSE(ctx, handle.resp.Body, onEvent)
}

// Complete invokes POST <BaseURL>/v1/messages with stream=false and returns the text and usage.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return CompleteResponse{}, errors.New("anthropic api key is required")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" && len(req.Metadata) > 0 {
		metaBytes, _ := json.Marshal(req.Metadata)
		prompt = string(metaBytes)
	}
	if prompt == "" {
		return CompleteResponse{}, errors.New("complete prompt must not be empty")
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    req.SystemPrompt,
		Messages: []anthropicMessage{
			{Role: RoleUser, Content: prompt},
		},
		Stream: false,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("marshal anthropic request: %w", err)
	}

	endpoint := p.cfg.BaseURL + "/v1/messages"
	parsed, err := resilience.CallWithRetry(
		ctx,
		p.breaker,
		p.retryAttempts,
		p.retryBackoff,
		p.cfg.Timeout,
		resilience.IsTransientHTTPError,
		func(attemptCtx context.Context) (anthropicCompleteResponse, error) {
			var out anthropicCompleteResponse
			httpReq, reqErr := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if reqErr != nil {
				return out, reqErr
			}
			p.applyHeaders(httpReq, false)

			resp, doErr := p.client.Do(httpReq)
			if doErr != nil {
				return out, doErr
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
				return out, &resilience.HTTPStatusError{
					StatusCode: resp.StatusCode,
					Body:       strings.TrimSpace(string(errBody)),
				}
			}

			if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil {
				return out, fmt.Errorf("decode anthropic response: %w", decErr)
			}
			return out, nil
		},
	)
	if err != nil {
		return CompleteResponse{}, err
	}

	var textBuilder strings.Builder
	for _, blk := range parsed.Content {
		if blk.Type == "text" {
			textBuilder.WriteString(blk.Text)
		}
	}

	usage := p.buildUsage(parsed.Usage.InputTokens, parsed.Usage.OutputTokens)
	return CompleteResponse{
		Text:  textBuilder.String(),
		Usage: usage,
	}, nil
}

func (p *AnthropicProvider) openStreamWithRetry(ctx context.Context, payload []byte) (*streamResponseHandle, error) {
	endpoint := p.cfg.BaseURL + "/v1/messages"
	return resilience.CallWithRetry(
		ctx,
		p.breaker,
		p.retryAttempts,
		p.retryBackoff,
		p.cfg.Timeout,
		resilience.IsTransientHTTPError,
		func(attemptCtx context.Context) (*streamResponseHandle, error) {
			// Bind the HTTP request to a stream-scoped context so CallWithRetry's
			// defer cancel(attemptCtx) does not kill the SSE body after headers arrive.
			streamCtx, streamCancel := context.WithTimeout(ctx, p.cfg.Timeout)
			stopForward := context.AfterFunc(attemptCtx, streamCancel)

			httpReq, reqErr := http.NewRequestWithContext(streamCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if reqErr != nil {
				stopForward()
				streamCancel()
				return nil, reqErr
			}
			p.applyHeaders(httpReq, true)

			//nolint:bodyclose // Closed on error paths here and on success via handle.close() in StreamChat.
			resp, doErr := p.client.Do(httpReq)
			if doErr != nil {
				stopForward()
				streamCancel()
				return nil, doErr
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
				_ = resp.Body.Close()
				stopForward()
				streamCancel()
				return nil, &resilience.HTTPStatusError{
					StatusCode: resp.StatusCode,
					Body:       strings.TrimSpace(string(errBody)),
				}
			}

			if !stopForward() {
				_ = resp.Body.Close()
				streamCancel()
				return nil, context.DeadlineExceeded
			}

			return &streamResponseHandle{resp: resp, cancel: streamCancel}, nil
		},
	)
}

func (p *AnthropicProvider) applyHeaders(req *http.Request, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.cfg.APIKey)
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
}

func (p *AnthropicProvider) buildChatPayload(req ChatRequest, stream bool) ([]byte, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	msgs := convertToAnthropicMessages(req.Messages)
	if len(msgs) == 0 {
		return nil, errors.New("chat messages must not be empty")
	}

	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    req.SystemPrompt,
		Messages:  msgs,
		Tools:     tools,
		Stream:    stream,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}
	return json.Marshal(body)
}

func convertToAnthropicMessages(messages []Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch m.Role {
		case RoleUser:
			out = append(out, anthropicMessage{
				Role:    RoleUser,
				Content: m.Content,
			})
		case RoleAssistant:
			if len(m.ToolCalls) == 0 {
				out = append(out, anthropicMessage{
					Role:    RoleAssistant,
					Content: m.Content,
				})
				continue
			}
			blocks := make([]anthropicContentBlock, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{
					Type: "text",
					Text: m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == nil {
					args = map[string]any{}
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: args,
				})
			}
			out = append(out, anthropicMessage{
				Role:    RoleAssistant,
				Content: blocks,
			})
		case RoleTool:
			// Merge consecutive tool results into a single user message per Anthropic API rules.
			var blocks []anthropicContentBlock
			for i < len(messages) && messages[i].Role == RoleTool {
				tm := messages[i]
				blocks = append(blocks, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: tm.ToolCallID,
					Content:   tm.Content,
					IsError:   tm.IsError,
				})
				i++
			}
			i--
			out = append(out, anthropicMessage{
				Role:    RoleUser,
				Content: blocks,
			})
		}
	}
	return out
}

type sseToolBlock struct {
	id         string
	name       string
	initialArg map[string]any
	jsonBuf    strings.Builder
}

type sseEnvelope struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type  string         `json:"type"`
		Text  string         `json:"text"`
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *AnthropicProvider) consumeSSE(ctx context.Context, body io.Reader, onEvent func(StreamEvent) error) (Usage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSEScannerBufBytes)

	var (
		inputTokens  int
		outputTokens int
		eventName    string
		dataLines    []string
		toolBlocks   = make(map[int]*sseToolBlock)
		stopped      bool
	)

	dispatch := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		raw := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		evType := eventName
		eventName = ""

		if raw == "" || raw == "[DONE]" {
			return nil
		}

		var env sseEnvelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			return fmt.Errorf("decode anthropic sse event: %w", err)
		}
		if env.Type != "" {
			evType = env.Type
		}

		switch evType {
		case "message_start":
			if env.Message != nil {
				if env.Message.Usage.InputTokens > 0 {
					inputTokens = env.Message.Usage.InputTokens
				}
				if env.Message.Usage.OutputTokens > 0 {
					outputTokens = env.Message.Usage.OutputTokens
				}
			}
		case "content_block_start":
			if env.ContentBlock != nil {
				switch env.ContentBlock.Type {
				case "tool_use":
					toolBlocks[env.Index] = &sseToolBlock{
						id:         env.ContentBlock.ID,
						name:       env.ContentBlock.Name,
						initialArg: env.ContentBlock.Input,
					}
				case "text":
					if env.ContentBlock.Text != "" && onEvent != nil {
						if err := onEvent(StreamEvent{Type: EventToken, Text: env.ContentBlock.Text}); err != nil {
							return err
						}
					}
				}
			}
		case "content_block_delta":
			if env.Delta != nil {
				switch env.Delta.Type {
				case "text_delta":
					if env.Delta.Text != "" && onEvent != nil {
						if err := onEvent(StreamEvent{Type: EventToken, Text: env.Delta.Text}); err != nil {
							return err
						}
					}
				case "input_json_delta":
					if blk, ok := toolBlocks[env.Index]; ok {
						blk.jsonBuf.WriteString(env.Delta.PartialJSON)
					}
				}
			}
		case "content_block_stop":
			if blk, ok := toolBlocks[env.Index]; ok {
				delete(toolBlocks, env.Index)
				args := map[string]any{}
				if len(blk.initialArg) > 0 {
					args = blk.initialArg
				}
				if rawJSON := strings.TrimSpace(blk.jsonBuf.String()); rawJSON != "" {
					var parsed map[string]any
					if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
						return fmt.Errorf("decode anthropic tool input json: %w", err)
					}
					args = parsed
				}
				if onEvent != nil {
					if err := onEvent(StreamEvent{
						Type: EventToolCall,
						ToolCall: &ToolCall{
							ID:        blk.id,
							Name:      blk.name,
							Arguments: args,
						},
					}); err != nil {
						return err
					}
				}
			}
		case "message_delta":
			if env.Usage != nil {
				if env.Usage.InputTokens > 0 {
					inputTokens = env.Usage.InputTokens
				}
				if env.Usage.OutputTokens > 0 {
					outputTokens = env.Usage.OutputTokens
				}
			}
		case "message_stop":
			stopped = true
		case "error":
			if env.Error != nil {
				return fmt.Errorf("anthropic stream error (%s): %s", env.Error.Type, env.Error.Message)
			}
			return errors.New("anthropic stream error")
		}
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return p.buildUsage(inputTokens, outputTokens), err
		}
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return p.buildUsage(inputTokens, outputTokens), err
			}
			if stopped {
				break
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return p.buildUsage(inputTokens, outputTokens), fmt.Errorf("read anthropic sse stream: %w", err)
	}
	if !stopped {
		if err := dispatch(); err != nil {
			return p.buildUsage(inputTokens, outputTokens), err
		}
	}

	return p.buildUsage(inputTokens, outputTokens), nil
}

func (p *AnthropicProvider) buildUsage(inputTokens, outputTokens int) Usage {
	return Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		CostVND:      EstimateCostVND(inputTokens, outputTokens, p.cfg.InputCostVNDPer1K, p.cfg.OutputCostVNDPer1K),
	}
}
