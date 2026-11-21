# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/chat/go.mod services/chat/go.sum* ./services/chat/

WORKDIR /src/services/chat
RUN go mod download

COPY services/chat ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chat ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/chat /usr/local/bin/chat

USER appuser

EXPOSE 50060 8090

ENTRYPOINT ["/usr/local/bin/chat"]
