# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go ./gen/go
COPY pkg/common ./pkg/common
COPY services/notification/go.mod services/notification/go.sum* ./services/notification/

WORKDIR /src/services/notification
RUN go mod download

COPY services/notification ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/notification ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/notification /usr/local/bin/notification

USER 10001:10001

EXPOSE 50058 8088

ENTRYPOINT ["/usr/local/bin/notification"]
