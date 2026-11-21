package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/config"
	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
	"github.com/nexusmart/core/services/assistant/internal/store"
	"github.com/nexusmart/core/services/assistant/internal/tools"
)

// Domain sentinel errors mapped to HTTP/gRPC status codes by transport layers.
var (
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrRateLimited     = errors.New("rate limit exceeded")
	ErrBudgetExceeded  = errors.New("daily cost budget exceeded")
	ErrAbuseLocked     = errors.New("temporarily locked due to repeated policy violations")
	ErrNotFound        = errors.New("not found")
)

// DomainError carries structured error metadata including optional RetryAfterSec.
type DomainError struct {
	Kind          error
	Code          string
	Message       string
	RetryAfterSec int64
}

func (e *DomainError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Kind != nil {
		return e.Kind.Error()
	}
	return e.Code
}

func (e *DomainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// RetryAfterSeconds extracts RetryAfterSec from err if it wraps a DomainError.
func RetryAfterSeconds(err error) int64 {
	var de *DomainError
	if errors.As(err, &de) && de.RetryAfterSec > 0 {
		return de.RetryAfterSec
	}
	return 0
}

// ErrorCode extracts the canonical machine-readable error code from err.
func ErrorCode(err error) string {
	var de *DomainError
	if errors.As(err, &de) && de.Code != "" {
		return de.Code
	}
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "UNAUTHENTICATED"
	case errors.Is(err, ErrForbidden):
		return "FORBIDDEN"
	case errors.Is(err, ErrInvalidArgument):
		return "INVALID_ARGUMENT"
	case errors.Is(err, ErrRateLimited):
		return "RATE_LIMITED"
	case errors.Is(err, ErrBudgetExceeded):
		return "BUDGET_EXCEEDED"
	case errors.Is(err, ErrAbuseLocked):
		return "ABUSE_LOCKED"
	case errors.Is(err, ErrNotFound):
		return "NOT_FOUND"
	default:
		return "INTERNAL_ERROR"
	}
}

func newDomainErr(kind error, code, msg string, retryAfterSec int64) error {
	return &DomainError{
		Kind:          kind,
		Code:          code,
		Message:       msg,
		RetryAfterSec: retryAfterSec,
	}
}

// Deps configures dependencies for Service.
type Deps struct {
	Config      config.Config
	LLM         llm.Provider
	FallbackLLM llm.Provider
	Guardrail   guardrail.Engine
	Tools       tools.Executor
	Products    retriever.ProductRetriever
	Policies    policy.Retriever
	Quota       quota.Manager
	Store       store.Store
	Metrics     *Metrics
	Logger      *slog.Logger
	Now         func() time.Time
}

// Service implements all AI assistant capabilities: SSE chat with RAG & tool calling,
// handoff to support agents, seller reports, product description drafting, seller reply
// suggestions, product review summarization, and feedback collection.
type Service struct {
	cfg         config.Config
	llm         llm.Provider
	fallbackLLM llm.Provider
	guardrail   guardrail.Engine
	tools       tools.Executor
	products    retriever.ProductRetriever
	policies    policy.Retriever
	quota       quota.Manager
	store       store.Store
	metrics     *Metrics
	logger      *slog.Logger
	now         func() time.Time
}

// New constructs a Service from Deps with safe defaults.
func New(deps Deps) *Service {
	cfg := deps.Config
	if cfg.MinReviewsSummary <= 0 {
		cfg.MinReviewsSummary = 10
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	if cfg.ReviewSummaryTTL <= 0 {
		cfg.ReviewSummaryTTL = time.Hour
	}
	if cfg.BusinessHourStart == 0 && cfg.BusinessHourEnd == 0 {
		cfg.BusinessHourStart = 8
		cfg.BusinessHourEnd = 22
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = "claude-opus-5-5"
	}

	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	m := deps.Metrics
	if m == nil {
		m = NewMetrics(nil)
	}

	return &Service{
		cfg:         cfg,
		llm:         deps.LLM,
		fallbackLLM: deps.FallbackLLM,
		guardrail:   deps.Guardrail,
		tools:       deps.Tools,
		products:    deps.Products,
		policies:    deps.Policies,
		quota:       deps.Quota,
		store:       deps.Store,
		metrics:     m,
		logger:      log,
		now:         nowFn,
	}
}

// FeedbackRequest carries user helpfulness feedback on an assistant answer (UC-AI13).
type FeedbackRequest struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Helpful        bool   `json:"helpful"`
	Comment        string `json:"comment"`
}

// SubmitFeedback stores buyer/seller feedback for quality evaluation.
func (s *Service) SubmitFeedback(ctx context.Context, userID string, req FeedbackRequest) error {
	if strings.TrimSpace(req.ConversationID) == "" && strings.TrimSpace(req.MessageID) == "" {
		return newDomainErr(ErrInvalidArgument, "INVALID_ARGUMENT", "conversation_id or message_id is required", 0)
	}
	if s.store == nil {
		return nil
	}
	return s.store.SaveFeedback(ctx, store.Feedback{
		ConversationID: strings.TrimSpace(req.ConversationID),
		MessageID:      strings.TrimSpace(req.MessageID),
		UserID:         strings.TrimSpace(userID),
		Helpful:        req.Helpful,
		Comment:        strings.TrimSpace(req.Comment),
		CreatedAt:      s.now().UTC(),
	})
}

func (s *Service) subjectKey(cc tools.CallContext, clientIP string) string {
	if strings.TrimSpace(cc.UserID) != "" {
		return "user:" + strings.TrimSpace(cc.UserID)
	}
	if strings.TrimSpace(clientIP) != "" {
		return "ip:" + strings.TrimSpace(clientIP)
	}
	return "anon"
}

func (s *Service) selectProvider(ctx context.Context) (llm.Provider, string, error) {
	if s.llm == nil {
		return nil, "", errors.New("llm provider not configured")
	}
	if s.quota != nil {
		dec, err := s.quota.CheckDailyCost(ctx)
		if err != nil {
			s.logger.WarnContext(ctx, "quota check daily cost failed; continuing with primary provider", slog.String("error", err.Error()))
		} else if !dec.Allowed {
			if s.fallbackLLM != nil {
				model := s.cfg.LLMFallbackModel
				if model == "" {
					model = s.fallbackLLM.Name()
				}
				return s.fallbackLLM, model, nil
			}
			return nil, "", newDomainErr(
				ErrBudgetExceeded,
				"BUDGET_EXCEEDED",
				"Hệ thống trợ lý AI đã đạt giới hạn ngân sách ngày hôm nay. Vui lòng thử lại sau.",
				dec.RetryAfterSec,
			)
		}
	}
	model := s.cfg.LLMModel
	if s.llm.Name() == "mock" {
		model = "mock"
	}
	return s.llm, model, nil
}

func (s *Service) checkAndReserveQuota(ctx context.Context, subject string, estimatedTokens int64) error {
	if s.quota == nil {
		return nil
	}
	if estimatedTokens <= 0 {
		estimatedTokens = 100
	}
	dec, err := s.quota.CheckAndReserveTokens(ctx, subject, estimatedTokens)
	if err != nil {
		s.logger.WarnContext(ctx, "quota check tokens failed; failing open", slog.String("error", err.Error()))
		return nil
	}
	if !dec.Allowed {
		return newDomainErr(
			ErrRateLimited,
			"RATE_LIMITED",
			"Bạn đã đạt hạn mức sử dụng trợ lý AI trong ngày. Vui lòng thử lại sau.",
			dec.RetryAfterSec,
		)
	}
	return nil
}

func (s *Service) reconcileQuota(ctx context.Context, subject string, estimatedTokens int64, usage llm.Usage) {
	if s.quota == nil {
		return
	}
	delta := int64(usage.TotalTokens) - estimatedTokens
	if delta != 0 {
		if err := s.quota.RecordActualTokens(ctx, subject, delta); err != nil {
			s.logger.WarnContext(ctx, "failed to reconcile actual tokens", slog.String("error", err.Error()))
		}
	}
	if usage.CostVND > 0 {
		if _, err := s.quota.RecordCostVND(ctx, usage.CostVND); err != nil {
			s.logger.WarnContext(ctx, "failed to record daily VND cost", slog.String("error", err.Error()))
		}
	}
}

func (s *Service) recordUsageLog(
	ctx context.Context,
	userID, shopID, endpoint, model string,
	usage llm.Usage,
	latencyMs, ttftMs int64,
	cacheHit bool,
	blockedReason string,
) {
	s.metrics.AddTokens(endpoint, model, usage.InputTokens, usage.OutputTokens)
	s.metrics.AddCostVND(endpoint, model, usage.CostVND)
	if s.store == nil {
		return
	}
	err := s.store.SaveUsageLog(ctx, store.UsageLog{
		TraceID:       telemetry.TraceIDFromContext(ctx),
		UserID:        userID,
		ShopID:        shopID,
		Endpoint:      endpoint,
		Model:         model,
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CostVND:       usage.CostVND,
		LatencyMs:     latencyMs,
		TTFTMs:        ttftMs,
		CacheHit:      cacheHit,
		BlockedReason: blockedReason,
		CreatedAt:     s.now().UTC(),
	})
	if err != nil {
		s.logger.WarnContext(ctx, "failed to persist assistant usage log", slog.String("error", err.Error()))
	}
}

func normalizeCacheQuery(q string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(q)))
	return strings.Join(fields, " ")
}

func hashCacheKey(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return prefix + ":" + hex.EncodeToString(h.Sum(nil))
}

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func formatVND(amount int64) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	raw := fmt.Sprintf("%d", amount)
	n := len(raw)
	if n <= 3 {
		if neg {
			return "-" + raw + "đ"
		}
		return raw + "đ"
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := n % 3
	if rem > 0 {
		b.WriteString(raw[:rem])
		b.WriteByte('.')
	}
	for i := rem; i < n; i += 3 {
		if i > rem {
			b.WriteByte('.')
		}
		b.WriteString(raw[i : i+3])
	}
	b.WriteString("đ")
	return b.String()
}
