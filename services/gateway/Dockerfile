# Multi-stage build for NexusMart API gateway (build context: repository root)
FROM golang:1.22-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/gateway/go.mod services/gateway/go.sum ./services/gateway/

WORKDIR /src/services/gateway
RUN go mod download

WORKDIR /src
COPY services/gateway ./services/gateway

WORKDIR /src/services/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

COPY --from=builder /out/gateway /usr/local/bin/gateway

USER appuser

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/gateway"]
