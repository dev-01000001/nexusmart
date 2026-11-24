# syntax=docker/dockerfile:1.7
# Multi-stage build for NexusMart Payment Service (Rust)
# Build context: repository root (so proto/ is accessible to tonic-build)

FROM rust:1-slim-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config \
    protobuf-compiler \
    libprotobuf-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Copy shared protobuf contracts, RBAC capabilities matrix, and payment service sources (never host target/)
COPY proto ./proto
COPY pkg/common/auth/capabilities.json ./pkg/common/auth/capabilities.json
COPY services/payment/Cargo.toml services/payment/Cargo.lock services/payment/build.rs ./services/payment/
COPY services/payment/src ./services/payment/src
COPY services/payment/migrations ./services/payment/migrations

WORKDIR /workspace/services/payment

RUN cargo build --release

# Final minimal runtime stage running as non-root user
FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 appuser \
    && useradd --uid 10001 --gid appuser --shell /sbin/nologin --create-home appuser

WORKDIR /app

COPY --from=builder /workspace/services/payment/target/release/nexusmart-payment /usr/local/bin/nexusmart-payment

USER 10001:10001

EXPOSE 8086 50056

ENV SERVICE_NAME=payment \
    HTTP_PORT=8086 \
    GRPC_PORT=50056 \
    RUST_LOG=info

ENTRYPOINT ["/usr/local/bin/nexusmart-payment"]
