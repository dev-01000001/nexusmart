package evalharness

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/services/assistant/internal/config"
	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/httpserver"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/store"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

// NewStack builds a complete in-process assistant stack backed by real guardrails,
// embedded policy RAG, in-memory hybrid product retriever, JWT-forwarding tool registry,
// memory quota/cache/store, real SSE HTTP server, and mock or Anthropic LLM provider.
func NewStack(cfg StackConfig) (*Stack, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "mock"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "claude-opus-5-5"
	}
	jwtSecret := strings.TrimSpace(cfg.JWTSecret)
	if jwtSecret == "" {
		jwtSecret = "eval-harness-jwt-secret"
	}
	tokenLimit := cfg.DailyUserTokenLimit
	if tokenLimit <= 0 {
		tokenLimit = 5_000_000
	}
	costLimit := cfg.DailyCostLimitVND
	if costLimit <= 0 {
		costLimit = 100_000_000
	}
	strikeLimit := cfg.AbuseStrikeLimit
	if strikeLimit <= 0 {
		strikeLimit = 1000
	}

	jwtMgr, err := auth.NewManager(jwtSecret, auth.DefaultAccessTTL)
	if err != nil {
		return nil, fmt.Errorf("init eval auth manager: %w", err)
	}

	guard := guardrail.New()
	polRAG, err := policy.NewRAG("", "mock")
	if err != nil {
		return nil, fmt.Errorf("init policy rag: %w", err)
	}

	hits := make([]retriever.ProductHit, 0, len(cfg.Products))
	for _, p := range cfg.Products {
		skus := make([]retriever.SKUHit, 0, len(p.SKUs))
		for _, s := range p.SKUs {
			skus = append(skus, retriever.SKUHit{
				ID:             s.ID,
				SKUCode:        s.SKUCode,
				Name:           s.Name,
				PriceVND:       s.PriceVND,
				AvailableStock: s.AvailableStock,
				Attributes:     s.Attributes,
			})
		}
		hits = append(hits, retriever.ProductHit{
			ID:          p.ID,
			ShopID:      p.ShopID,
			ShopName:    p.ShopName,
			CategoryID:  p.CategoryID,
			Category:    p.Category,
			Name:        p.Name,
			Description: p.Description,
			MinPriceVND: p.MinPriceVND,
			MaxPriceVND: p.MaxPriceVND,
			ImageURL:    p.ImageURL,
			InStock:     p.InStock,
			SKUs:        skus,
		})
	}

	prodRet := retriever.NewInMemoryRetriever(hits, guard)
	log := logger.New("assistant-eval", logger.WithWriter(io.Discard))
	toolReg := tools.NewRegistry(prodRet, polRAG, cfg.Gateway, guard, log)
	quotaMgr := quota.NewMemoryManager(quota.Config{
		DailyUserTokenLimit: tokenLimit,
		DailyCostLimitVND:   costLimit,
		AbuseStrikeLimit:    strikeLimit,
		AbuseLockoutWindow:  15 * time.Minute,
	})
	memStore := store.NewMemoryStore()

	var provider llm.Provider
	switch mode {
	case "mock":
		provider = llm.NewMockProvider(380, 1900)
	case "anthropic":
		if strings.TrimSpace(cfg.AnthropicAPIKey) == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for --mode=anthropic")
		}
		ap, err := llm.NewAnthropicProvider(llm.AnthropicConfig{
			BaseURL:            cfg.AnthropicBaseURL,
			APIKey:             cfg.AnthropicAPIKey,
			Model:              model,
			Timeout:            45 * time.Second,
			InputCostVNDPer1K:  380,
			OutputCostVNDPer1K: 1900,
		})
		if err != nil {
			return nil, fmt.Errorf("init anthropic provider: %w", err)
		}
		provider = ap
	default:
		return nil, fmt.Errorf("unsupported llm mode %q (want mock or anthropic)", mode)
	}

	reg := prometheus.NewRegistry()
	svcCfg := config.Load()
	svcCfg.LLMMode = mode
	svcCfg.LLMModel = model
	svcCfg.DailyUserTokenLimit = tokenLimit
	svcCfg.DailyCostLimitVND = costLimit
	svcCfg.AbuseStrikeLimit = strikeLimit
	svcCfg.CacheTTL = 0 // disable cache across independent eval cases unless desired

	svc := service.New(service.Deps{
		Config:    svcCfg,
		LLM:       provider,
		Guardrail: guard,
		Tools:     toolReg,
		Products:  prodRet,
		Policies:  polRAG,
		Quota:     quotaMgr,
		Store:     memStore,
		Metrics:   service.NewMetrics(reg),
		Logger:    log,
	})

	handler := httpserver.New(httpserver.Config{
		ServiceName: "assistant-eval",
		Service:     svc,
		AuthManager: jwtMgr,
		RED:         metrics.NewRED("assistant-eval", reg),
		Gatherer:    reg,
		Logger:      log,
	})

	return &Stack{
		Handler: handler,
		signTokenFn: func(userID, role, shopID string) (string, error) {
			tok, _, _, err := jwtMgr.SignAccessToken(userID, role, shopID)
			return tok, err
		},
		suggestReplyFn: func(ctx context.Context, userID, shopID, rawBearer string, req SuggestReplyReq) (*SuggestReplyResp, error) {
			history := make([]service.ChatHistoryMessage, 0, len(req.History))
			for _, h := range req.History {
				history = append(history, service.ChatHistoryMessage{
					Sender: h.Sender,
					Body:   h.Body,
				})
			}
			res, err := svc.SuggestReply(ctx, tools.CallContext{
				UserID:       userID,
				UserRole:     "seller",
				SellerShopID: shopID,
				ShopID:       shopID,
				RawBearer:    rawBearer,
			}, service.SuggestReplyRequest{
				ConversationID:  req.ConversationID,
				ProductID:       req.ProductID,
				CustomerMessage: req.CustomerMessage,
				History:         history,
			})
			if err != nil {
				return nil, err
			}
			out := make([]ReplySuggestion, 0, len(res.Suggestions))
			for _, s := range res.Suggestions {
				out = append(out, ReplySuggestion{
					ID:   s.ID,
					Text: s.Text,
					Tone: s.Tone,
				})
			}
			return &SuggestReplyResp{
				Suggestions:       out,
				NeedsMoreInfo:     res.NeedsMoreInfo,
				InjectionDetected: res.InjectionDetected,
				AIGenerated:       res.AIGenerated,
			}, nil
		},
		verifyGroundingFn: func(text, allowedShopID string, retrieved []GroundedProduct) (bool, []string) {
			gp := make([]guardrail.GroundedProduct, 0, len(retrieved))
			for _, r := range retrieved {
				gp = append(gp, guardrail.GroundedProduct{
					ID:     r.ID,
					ShopID: r.ShopID,
					Name:   r.Name,
				})
			}
			return guard.VerifyGrounding(text, allowedShopID, gp)
		},
	}, nil
}
