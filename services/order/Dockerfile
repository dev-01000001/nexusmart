# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src

# Copy shared modules and order service module manifests first for layer caching.
COPY gen/go/go.mod gen/go/go.sum ./gen/go/
COPY pkg/common/go.mod pkg/common/go.sum ./pkg/common/
COPY services/order/go.mod services/order/go.sum ./services/order/

WORKDIR /src/services/order
RUN go mod download

# Copy source code for shared modules and order service.
WORKDIR /src
COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/order ./services/order

WORKDIR /src/services/order
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/order-server ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 -H appuser

WORKDIR /app
COPY --from=builder /out/order-server /app/order-server

USER 10001:10001

EXPOSE 50055 8085

ENTRYPOINT ["/app/order-server"]
