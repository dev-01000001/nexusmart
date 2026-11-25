# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum ./gen/go/
COPY pkg/common/go.mod pkg/common/go.sum ./pkg/common/
COPY services/promotion/go.mod services/promotion/go.sum ./services/promotion/

WORKDIR /src/services/promotion
RUN go mod download

WORKDIR /src
COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/promotion ./services/promotion

WORKDIR /src/services/promotion
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/promotion ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 -H appuser

COPY --from=builder /out/promotion /usr/local/bin/promotion

USER 10001:10001

EXPOSE 50054 8084

ENTRYPOINT ["/usr/local/bin/promotion"]
