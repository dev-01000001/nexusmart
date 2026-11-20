FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum /src/gen/go/
COPY pkg/common/go.mod pkg/common/go.sum /src/pkg/common/
COPY services/cart/go.mod services/cart/go.sum /src/services/cart/

WORKDIR /src/services/cart
RUN go mod download

WORKDIR /src
COPY gen/go /src/gen/go
COPY pkg/common /src/pkg/common
COPY services/cart /src/services/cart

WORKDIR /src/services/cart
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cart ./cmd/cart

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/cart /usr/local/bin/cart

USER appuser

EXPOSE 50053 8083

ENTRYPOINT ["/usr/local/bin/cart"]
