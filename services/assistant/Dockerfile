# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/assistant/go.mod services/assistant/go.sum* ./services/assistant/

WORKDIR /src/services/assistant
RUN go mod download

COPY services/assistant ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/assistant ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/assistant /usr/local/bin/assistant

USER 10001:10001

EXPOSE 50061 8091

ENTRYPOINT ["/usr/local/bin/assistant"]
