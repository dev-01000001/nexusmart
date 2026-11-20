package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RED holds Prometheus Rate, Errors, and Duration collectors for HTTP and gRPC servers.
type RED struct {
	service string

	httpRequestsTotal   *prometheus.CounterVec
	httpErrorsTotal     *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec

	grpcRequestsTotal   *prometheus.CounterVec
	grpcErrorsTotal     *prometheus.CounterVec
	grpcRequestDuration *prometheus.HistogramVec
}

// NewRED registers RED metrics for service on reg (defaults to prometheus.DefaultRegisterer).
func NewRED(service string, reg prometheus.Registerer) *RED {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	r := &RED{
		service: service,
		httpRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests handled.",
		}, []string{"service", "method", "route", "status"}),
		httpErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_errors_total",
			Help: "Total number of HTTP 5xx error responses.",
		}, []string{"service", "method", "route", "status"}),
		httpRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service", "method", "route"}),
		grpcRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_requests_total",
			Help: "Total number of gRPC unary requests handled.",
		}, []string{"service", "method", "code"}),
		grpcErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_errors_total",
			Help: "Total number of gRPC unary requests returning a non-OK status.",
		}, []string{"service", "method", "code"}),
		grpcRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grpc_request_duration_seconds",
			Help:    "gRPC unary request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service", "method"}),
	}

	reg.MustRegister(
		r.httpRequestsTotal,
		r.httpErrorsTotal,
		r.httpRequestDuration,
		r.grpcRequestsTotal,
		r.grpcErrorsTotal,
		r.grpcRequestDuration,
	)

	return r
}

// HTTPMiddleware records HTTP RED metrics compatible with chi routers.
func (r *RED) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(sw, req)

			route := req.URL.Path
			if rctx := chi.RouteContext(req.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					route = pat
				} else {
					route = "unmatched"
				}
			}

			statusStr := strconv.Itoa(sw.status)
			r.httpRequestsTotal.WithLabelValues(r.service, req.Method, route, statusStr).Inc()
			if sw.status >= http.StatusInternalServerError {
				r.httpErrorsTotal.WithLabelValues(r.service, req.Method, route, statusStr).Inc()
			}
			r.httpRequestDuration.WithLabelValues(r.service, req.Method, route).Observe(time.Since(start).Seconds())
		})
	}
}

// GRPCUnaryServerInterceptor records gRPC RED metrics for unary RPCs.
func (r *RED) GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}
		codeStr := code.String()
		r.grpcRequestsTotal.WithLabelValues(r.service, info.FullMethod, codeStr).Inc()
		if code != codes.OK {
			r.grpcErrorsTotal.WithLabelValues(r.service, info.FullMethod, codeStr).Inc()
		}
		r.grpcRequestDuration.WithLabelValues(r.service, info.FullMethod).Observe(time.Since(start).Seconds())

		return resp, err
	}
}

// NewOpsHandler builds a standard HTTP mux serving /metrics, /healthz, and /readyz.
func NewOpsHandler(service string, readyFn func(context.Context) error, gatherer prometheus.Gatherer) http.Handler {
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": service,
		})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if readyFn != nil {
			if err := readyFn(req.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status":  "not_ready",
					"service": service,
					"error":   err.Error(),
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ready",
			"service": service,
		})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush delegates to the underlying http.Flusher so SSE streams flush immediately.
func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController.
func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
