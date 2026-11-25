package service

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds custom Prometheus collectors for the AI assistant service.
type Metrics struct {
	TTFTSeconds          *prometheus.HistogramVec
	TokensTotal          *prometheus.CounterVec
	CostVNDTotal         *prometheus.CounterVec
	CacheTotal           *prometheus.CounterVec
	GuardrailBlocksTotal *prometheus.CounterVec
	ToolCallsTotal       *prometheus.CounterVec
}

// NewMetrics builds assistant-specific Prometheus metrics and registers them on reg when non-nil.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		TTFTSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "assistant_ttft_seconds",
			Help:    "Time to first emitted SSE token in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 1.5, 2.0, 3.0, 5.0, 10.0},
		}, []string{"endpoint", "cache_hit", "model"}),
		TokensTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "assistant_tokens_total",
			Help: "Total LLM tokens consumed by direction (input/output).",
		}, []string{"endpoint", "direction", "model"}),
		CostVNDTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "assistant_cost_vnd_total",
			Help: "Total estimated LLM cost in integer VND.",
		}, []string{"endpoint", "model"}),
		CacheTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "assistant_cache_requests_total",
			Help: "Total assistant cache lookups by outcome (hit/miss).",
		}, []string{"endpoint", "result"}),
		GuardrailBlocksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "assistant_guardrail_blocks_total",
			Help: "Total guardrail interventions by category and pipeline stage.",
		}, []string{"category", "stage"}),
		ToolCallsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "assistant_tool_calls_total",
			Help: "Total assistant tool executions by tool name and outcome.",
		}, []string{"tool", "status"}),
	}

	if reg != nil {
		reg.MustRegister(
			m.TTFTSeconds,
			m.TokensTotal,
			m.CostVNDTotal,
			m.CacheTotal,
			m.GuardrailBlocksTotal,
			m.ToolCallsTotal,
		)
	}
	return m
}

// ObserveTTFT records SSE time-to-first-token latency.
func (m *Metrics) ObserveTTFT(endpoint string, cacheHit bool, model string, d time.Duration) {
	if m == nil || m.TTFTSeconds == nil {
		return
	}
	if model == "" {
		model = "unknown"
	}
	m.TTFTSeconds.WithLabelValues(endpoint, strconv.FormatBool(cacheHit), model).Observe(d.Seconds())
}

// AddTokens increments input and output token counters.
func (m *Metrics) AddTokens(endpoint, model string, input, output int) {
	if m == nil || m.TokensTotal == nil {
		return
	}
	if model == "" {
		model = "unknown"
	}
	if input > 0 {
		m.TokensTotal.WithLabelValues(endpoint, "input", model).Add(float64(input))
	}
	if output > 0 {
		m.TokensTotal.WithLabelValues(endpoint, "output", model).Add(float64(output))
	}
}

// AddCostVND increments the integer VND LLM cost counter.
func (m *Metrics) AddCostVND(endpoint, model string, cost int64) {
	if m == nil || m.CostVNDTotal == nil || cost <= 0 {
		return
	}
	if model == "" {
		model = "unknown"
	}
	m.CostVNDTotal.WithLabelValues(endpoint, model).Add(float64(cost))
}

// IncCache records a cache hit or miss.
func (m *Metrics) IncCache(endpoint string, hit bool) {
	if m == nil || m.CacheTotal == nil {
		return
	}
	res := "miss"
	if hit {
		res = "hit"
	}
	m.CacheTotal.WithLabelValues(endpoint, res).Inc()
}

// IncGuardrailBlock records a guardrail block or sanitization event.
func (m *Metrics) IncGuardrailBlock(category, stage string) {
	if m == nil || m.GuardrailBlocksTotal == nil {
		return
	}
	if category == "" {
		category = "unknown"
	}
	m.GuardrailBlocksTotal.WithLabelValues(category, stage).Inc()
}

// IncToolCall records a tool call outcome.
func (m *Metrics) IncToolCall(tool, status string) {
	if m == nil || m.ToolCallsTotal == nil {
		return
	}
	m.ToolCallsTotal.WithLabelValues(tool, status).Inc()
}
