package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const redactedMask = "[REDACTED]"

var sensitiveKeySubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"api_key",
	"apikey",
	"otp",
	"signature",
	"cookie",
	"private_key",
	"credit_card",
	"cvv",
	"hmac",
	"jwt",
	"dsn",
	"database_url",
	"credential",
	"street_address",
}

var (
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-~+/]+=*`)
	dsnPattern    = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:)([^@\s/]+)(@)`)
)

// Option customizes a logger instance.
type Option func(*options)

type options struct {
	writer io.Writer
	level  slog.Leveler
}

// WithWriter overrides the output writer (defaults to os.Stdout).
func WithWriter(w io.Writer) Option {
	return func(o *options) {
		if w != nil {
			o.writer = w
		}
	}
}

// WithLevel overrides the minimum log level (defaults to slog.LevelInfo).
func WithLevel(level slog.Leveler) Option {
	return func(o *options) {
		if level != nil {
			o.level = level
		}
	}
}

// New creates a JSON slog.Logger for the given service that injects OTel trace_id/span_id
// from context and redacts sensitive keys, embedded Bearer tokens/DSN credentials, and PII fields.
func New(service string, opts ...Option) *slog.Logger {
	cfg := options{
		writer: os.Stdout,
		level:  slog.LevelInfo,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	base := slog.NewJSONHandler(cfg.writer, &slog.HandlerOptions{
		Level: cfg.level,
	})
	h := &redactingTraceHandler{next: base}
	return slog.New(h).With(slog.String("service", service))
}

// MaskEmail masks the local part of an email address while preserving domain for debugging.
func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return redactedMask
	}
	local := []rune(email[:at])
	domain := email[at:]
	if len(local) <= 2 {
		return "*" + domain
	}
	return string(local[:2]) + "***" + domain
}

// MaskPhone masks the middle characters of a phone number.
func MaskPhone(phone string) string {
	runes := []rune(strings.TrimSpace(phone))
	if len(runes) <= 4 {
		return redactedMask
	}
	if len(runes) <= 6 {
		return string(runes[:1]) + "***" + string(runes[len(runes)-2:])
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-3:])
}

// ScrubSecrets replaces embedded Bearer tokens and URL/DSN passwords in arbitrary text.
func ScrubSecrets(value string) string {
	scrubbed := bearerPattern.ReplaceAllString(value, "Bearer "+redactedMask)
	scrubbed = dsnPattern.ReplaceAllString(scrubbed, "${1}"+redactedMask+"${3}")
	return scrubbed
}

// RedactValue redacts or masks a value based on its attribute key and string content.
func RedactValue(key, value string) string {
	lowerKey := strings.ToLower(key)
	for _, sub := range sensitiveKeySubstrings {
		if strings.Contains(lowerKey, sub) {
			return redactedMask
		}
	}
	if strings.Contains(lowerKey, "email") {
		return MaskEmail(value)
	}
	if strings.Contains(lowerKey, "phone") {
		return MaskPhone(value)
	}
	return ScrubSecrets(value)
}

type redactingTraceHandler struct {
	next slog.Handler
}

func (h *redactingTraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, ScrubSecrets(r.Message), r.PC)

	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		clean.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(sanitizeAttr(a))
		return true
	})

	return h.next.Handle(ctx, clean)
}

func (h *redactingTraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = sanitizeAttr(a)
	}
	return &redactingTraceHandler{next: h.next.WithAttrs(sanitized)}
}

func (h *redactingTraceHandler) WithGroup(name string) slog.Handler {
	return &redactingTraceHandler{next: h.next.WithGroup(name)}
}

func sanitizeAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		groupAttrs := v.Group()
		cleaned := make([]slog.Attr, len(groupAttrs))
		for i, ga := range groupAttrs {
			cleaned[i] = sanitizeAttr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(cleaned...)}
	}

	lowerKey := strings.ToLower(a.Key)
	for _, sub := range sensitiveKeySubstrings {
		if strings.Contains(lowerKey, sub) {
			return slog.String(a.Key, redactedMask)
		}
	}

	if strings.Contains(lowerKey, "email") {
		return slog.String(a.Key, MaskEmail(attrAsString(v)))
	}
	if strings.Contains(lowerKey, "phone") {
		return slog.String(a.Key, MaskPhone(attrAsString(v)))
	}

	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, ScrubSecrets(v.String()))
	case slog.KindAny:
		switch val := v.Any().(type) {
		case error:
			return slog.String(a.Key, ScrubSecrets(val.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, ScrubSecrets(val.String()))
		default:
			if val != nil {
				raw := fmt.Sprint(val)
				if scrubbed := ScrubSecrets(raw); scrubbed != raw {
					return slog.String(a.Key, scrubbed)
				}
			}
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

func attrAsString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindAny:
		switch val := v.Any().(type) {
		case error:
			return val.Error()
		case fmt.Stringer:
			return val.String()
		default:
			return fmt.Sprint(val)
		}
	default:
		return v.String()
	}
}
