# syntax=docker/dockerfile:1.7
# Multi-stage build for NexusMart AIOps Engine (Rust)
# Build context: repository root (matching services/payment/Dockerfile)

FROM rust:1-slim-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Copy only aiops sources (never host target/)
COPY services/aiops/Cargo.toml services/aiops/Cargo.lock* ./services/aiops/
COPY services/aiops/src ./services/aiops/src

WORKDIR /workspace/services/aiops

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

COPY --from=builder /workspace/services/aiops/target/release/nexusmart-aiops /usr/local/bin/nexusmart-aiops

USER 10001:10001

EXPOSE 8092

ENV SERVICE_NAME=aiops \
    HTTP_PORT=8092 \
    RUST_LOG=info

ENTRYPOINT ["/usr/local/bin/nexusmart-aiops"]
