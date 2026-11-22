# syntax=docker/dockerfile:1
# Multi-stage build for NexusMart Web Frontend (Next.js standalone)
# Build context: repository root (consistent with all backend services)

FROM node:24-alpine AS deps
WORKDIR /app/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

FROM node:24-alpine AS builder
WORKDIR /app/web

ENV NEXT_TELEMETRY_DISABLED=1

COPY --from=deps /app/web/node_modules ./node_modules
COPY web/ ./

RUN npm run build

FROM node:24-alpine AS runner
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

RUN addgroup -S -g 10001 nodejs \
    && adduser -S -u 10001 -G nodejs nextjs \
    && mkdir -p /app/.next \
    && chown -R nextjs:nodejs /app

COPY --from=builder --chown=nextjs:nodejs /app/web/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/web/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/web/.next/static ./.next/static

USER nextjs

EXPOSE 3000

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD wget -qO /dev/null http://127.0.0.1:3000/ || exit 1

CMD ["node", "server.js"]
