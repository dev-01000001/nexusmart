package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestHTTPAndGRPCTracePropagation(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{
		ServiceName: "test-service",
		SampleRatio: 1.0,
	})
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	// 1. HTTP middleware extracts incoming W3C traceparent and echoes X-Trace-Id
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const incomingTraceparent = "00-" + incomingTraceID + "-00f067aa0ba902b7-01"

	var handlerTraceID string
	var natsHeaders map[string]string
	h := HTTPMiddleware("gateway")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerTraceID = TraceIDFromContext(r.Context())
		natsHeaders = make(map[string]string)
		InjectTraceHeaders(r.Context(), natsHeaders)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	req.Header.Set("traceparent", incomingTraceparent)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if handlerTraceID != incomingTraceID {
		t.Fatalf("HTTP handler trace_id = %q, want %q", handlerTraceID, incomingTraceID)
	}
	if rr.Header().Get("X-Trace-Id") != incomingTraceID {
		t.Fatalf("X-Trace-Id header = %q, want %q", rr.Header().Get("X-Trace-Id"), incomingTraceID)
	}

	// 2. NATS/map header round-trip preserves trace ID
	extractedCtx := ExtractTraceHeaders(context.Background(), natsHeaders)
	if got := TraceIDFromContext(extractedCtx); got != incomingTraceID {
		t.Fatalf("NATS extracted trace_id = %q, want %q", got, incomingTraceID)
	}

	// 3. gRPC client -> server interceptor propagation preserves trace ID
	clientInt := GRPCUnaryClientInterceptor("gateway")
	serverInt := GRPCUnaryServerInterceptor("order")

	var serverTraceID string
	err = clientInt(extractedCtx, "/nexusmart.order.v1.OrderService/GetOrder", "req", nil, nil,
		func(outCtx context.Context, method string, req, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			outMD, _ := metadata.FromOutgoingContext(outCtx)
			inCtx := metadata.NewIncomingContext(context.Background(), outMD)
			_, srvErr := serverInt(inCtx, req, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
				serverTraceID = TraceIDFromContext(ctx)
				return "ok", nil
			})
			return srvErr
		})
	if err != nil {
		t.Fatalf("gRPC chain error: %v", err)
	}
	if serverTraceID != incomingTraceID {
		t.Fatalf("gRPC server trace_id = %q, want %q", serverTraceID, incomingTraceID)
	}
}

func TestHTTPMiddlewareSupportsSSEFlush(t *testing.T) {
	h := HTTPMiddleware("gateway")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ok\n\n"))
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Errorf("ResponseController.Flush failed: %v", err)
		}
		if flusher, ok := w.(http.Flusher); !ok {
			t.Errorf("ResponseWriter does not implement http.Flusher")
		} else {
			flusher.Flush()
		}
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/payments/p1/events", nil))
	if !rr.Flushed {
		t.Fatalf("expected ResponseRecorder to be flushed")
	}
}

func TestStartConsumerSpanLinksParentTrace(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{
		ServiceName: "notification",
		SampleRatio: 1.0,
	})
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	const (
		traceID      = "4bf92f3577b34da6a3ce929d0e0e4736"
		parentSpanID = "00f067aa0ba902b7"
	)
	ctx, span := StartConsumerSpan(context.Background(), "notification", "order.created", map[string]string{
		"traceparent": "00-" + traceID + "-" + parentSpanID + "-01",
	})
	defer span.End()

	if got := TraceIDFromContext(ctx); got != traceID {
		t.Fatalf("TraceIDFromContext = %q, want %q", got, traceID)
	}
	if got := SpanIDFromContext(ctx); got == "" || got == parentSpanID {
		t.Fatalf("SpanIDFromContext = %q, want new child span_id != %q", got, parentSpanID)
	}
}
