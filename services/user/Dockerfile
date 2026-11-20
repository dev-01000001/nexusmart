# Multi-stage build for NexusMart user service (build context: repository root)
FROM golang:1.22-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/user/go.mod services/user/go.sum ./services/user/

WORKDIR /src/services/user
RUN go mod download

WORKDIR /src
COPY services/user ./services/user

WORKDIR /src/services/user
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/user ./cmd/user

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

COPY --from=builder /out/user /usr/local/bin/user

USER appuser

EXPOSE 50051 8081

ENTRYPOINT ["/usr/local/bin/user"]
