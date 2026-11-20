FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum /src/gen/go/
COPY pkg/common/go.mod pkg/common/go.sum /src/pkg/common/
COPY services/product/go.mod services/product/go.sum /src/services/product/

WORKDIR /src/services/product
RUN go mod download

WORKDIR /src
COPY gen/go /src/gen/go
COPY pkg/common /src/pkg/common
COPY services/product /src/services/product

WORKDIR /src/services/product
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/product ./cmd/product

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 appuser

COPY --from=builder /out/product /usr/local/bin/product

USER appuser

EXPOSE 50052 8082

ENTRYPOINT ["/usr/local/bin/product"]
