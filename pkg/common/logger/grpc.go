package logger

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCUnaryServerInterceptor logs one structured JSON line per unary gRPC request with
// method, gRPC status code, duration_ms, and OTel trace_id/span_id (injected from ctx
// by the logger's trace handler). Request/response payloads, metadata headers, and raw
// error messages are deliberately omitted to prevent PII or secret leakage.
func GRPCUnaryServerInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if log == nil {
			return handler(ctx, req)
		}

		start := time.Now()
		resp, err := handler(ctx, req)

		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}

		method := ""
		if info != nil {
			method = info.FullMethod
		}

		durationMs := float64(time.Since(start).Microseconds()) / 1000.0
		log.LogAttrs(
			ctx,
			grpcCodeLogLevel(code),
			"grpc request",
			slog.String("method", method),
			slog.String("code", code.String()),
			slog.Float64("duration_ms", durationMs),
		)

		return resp, err
	}
}

func grpcCodeLogLevel(code codes.Code) slog.Level {
	switch code {
	case codes.OK:
		return slog.LevelInfo
	case codes.Internal, codes.Unknown, codes.DataLoss, codes.Unavailable, codes.DeadlineExceeded, codes.Unimplemented:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
