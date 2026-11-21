FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum /src/gen/go/
COPY pkg/common/go.mod pkg/common/go.sum /src/pkg/common/
COPY services/search/go.mod services/search/go.sum /src/services/search/

WORKDIR /src/services/search
RUN go mod download

WORKDIR /src
COPY gen/go /src/gen/go
COPY pkg/common /src/pkg/common
COPY services/search /src/services/search

WORKDIR /src/services/search
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/search ./cmd/search

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/search /usr/local/bin/search

USER appuser

EXPOSE 50059 8089

ENTRYPOINT ["/usr/local/bin/search"]
