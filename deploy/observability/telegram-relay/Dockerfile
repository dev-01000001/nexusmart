FROM node:22-alpine

WORKDIR /app

COPY server.mjs /app/server.mjs

ENV NODE_ENV=production \
    PORT=8099

USER node

EXPOSE 8099

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1

CMD ["node", "/app/server.mjs"]
