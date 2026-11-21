# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/shipping/go.mod services/shipping/go.sum* ./services/shipping/

WORKDIR /src/services/shipping
RUN go mod download

COPY services/shipping ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/shipping ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/shipping /usr/local/bin/shipping

USER 10001:10001

EXPOSE 50057 8087

ENTRYPOINT ["/usr/local/bin/shipping"]
