import http from "node:http";
import { randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";

const DEFAULT_PORT = 8099;
const DEFAULT_API_BASE_URL = "https://api.telegram.org";
const DEFAULT_TIMEOUT_MS = 5000;
const DEFAULT_MAX_RETRIES = 3;
const DEFAULT_RETRY_BASE_MS = 250;
const MAX_STORED_ALERTS = 200;
const MAX_BODY_BYTES = 1024 * 1024;
const TELEGRAM_MAX_MESSAGE_LEN = 4000;

const PLACEHOLDER_VALUES = new Set([
  "mock",
  "changeme",
  "change-me",
  "change_me",
  "placeholder",
  "dummy",
  "fake",
  "none",
  "null",
  "unset",
  "test",
  "your_bot_token",
  "your-bot-token",
  "your_chat_id",
  "your-chat-id",
  "telegram_bot_token",
  "telegram_chat_id",
]);

function isPlaceholder(value) {
  if (typeof value !== "string") return true;
  const trimmed = value.trim();
  if (!trimmed) return true;
  const lower = trimmed.toLowerCase();
  if (PLACEHOLDER_VALUES.has(lower)) return true;
  if (lower.startsWith("<") || lower.startsWith("replace")) return true;
  return false;
}

export function isLiveTelegramConfigured(botToken, chatId) {
  return !isPlaceholder(botToken) && !isPlaceholder(chatId);
}

export function maskSecret(input, secretToken = "") {
  let out = typeof input === "string" ? input : String(input ?? "");
  if (secretToken && secretToken.trim()) {
    out = out.split(secretToken.trim()).join("[REDACTED]");
  }
  out = out.replace(/\/bot[^/\s?#]+/gi, "/bot[REDACTED]");
  out = out.replace(/\bbot\d+:[A-Za-z0-9_-]{8,}/gi, "bot[REDACTED]");
  out = out.replace(/\b\d{6,}:[A-Za-z0-9_-]{16,}/g, "[REDACTED]");
  return out;
}

function normalizeAlerts(payload) {
  if (payload && Array.isArray(payload.alerts) && payload.alerts.length > 0) {
    return {
      status: String(payload.status || payload.alerts[0]?.status || "firing").toLowerCase(),
      receiver: String(payload.receiver || "telegram-relay"),
      alerts: payload.alerts.map((a) => ({
        status: String(a?.status || payload.status || "firing").toLowerCase(),
        labels: a?.labels && typeof a.labels === "object" ? a.labels : {},
        annotations: a?.annotations && typeof a.annotations === "object" ? a.annotations : {},
        startsAt: a?.startsAt || "",
        endsAt: a?.endsAt || "",
      })),
    };
  }

  // Direct alert JSON fallback (e.g. from AIOps or ad-hoc tests)
  const labels = {
    ...(payload?.labels && typeof payload.labels === "object" ? payload.labels : {}),
  };
  if (payload?.alertname && !labels.alertname) labels.alertname = String(payload.alertname);
  if (payload?.service && !labels.service) labels.service = String(payload.service);
  if (payload?.severity && !labels.severity) labels.severity = String(payload.severity);

  const annotations = {
    ...(payload?.annotations && typeof payload.annotations === "object" ? payload.annotations : {}),
  };
  if (payload?.summary && !annotations.summary) annotations.summary = String(payload.summary);
  if (payload?.title && !annotations.summary) annotations.summary = String(payload.title);
  if (payload?.description && !annotations.description) {
    annotations.description = String(payload.description);
  }
  if (payload?.message && !annotations.description) {
    annotations.description = String(payload.message);
  }
  if (payload?.text && !annotations.description) annotations.description = String(payload.text);

  return {
    status: String(payload?.status || "firing").toLowerCase(),
    receiver: String(payload?.receiver || "direct"),
    alerts: [
      {
        status: String(payload?.status || "firing").toLowerCase(),
        labels,
        annotations,
        startsAt: payload?.startsAt || new Date().toISOString(),
        endsAt: payload?.endsAt || "",
      },
    ],
  };
}

export function formatAlertmanagerPayload(payload) {
  const normalized = normalizeAlerts(payload);
  const tag = normalized.status === "resolved" ? "RESOLVED" : "FIRING";
  const services = [
    ...new Set(normalized.alerts.map((a) => a.labels.service || a.labels.dependency || "unknown")),
  ];
  const alertnames = [
    ...new Set(normalized.alerts.map((a) => a.labels.alertname || "NexusMartAlert")),
  ];

  const lines = [
    `[${tag}] NexusMart Alert (${normalized.alerts.length})`,
    `Service: ${services.join(", ")}`,
    `Alerts: ${alertnames.join(", ")}`,
    "",
  ];

  for (const alert of normalized.alerts) {
    const aStatus = alert.status.toUpperCase();
    const name = alert.labels.alertname || "Alert";
    const svc = alert.labels.service || alert.labels.dependency || "unknown";
    const sev = alert.labels.severity || "info";
    const summary = alert.annotations.summary || "";
    const description = alert.annotations.description || "";

    lines.push(`- [${aStatus}] ${name} (service=${svc}, severity=${sev})`);
    if (summary) lines.push(`  Summary: ${summary}`);
    if (description && description !== summary) lines.push(`  Detail: ${description}`);
    if (alert.startsAt) lines.push(`  Started: ${alert.startsAt}`);
  }

  let text = lines.join("\n").trim();
  if (text.length > TELEGRAM_MAX_MESSAGE_LEN) {
    text = `${text.slice(0, TELEGRAM_MAX_MESSAGE_LEN - 15)}\n...[truncated]`;
  }

  return {
    status: normalized.status,
    receiver: normalized.receiver,
    services,
    alertnames,
    alerts: normalized.alerts,
    text,
  };
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function sendTelegramMessage({
  botToken,
  chatId,
  text,
  apiBaseUrl = DEFAULT_API_BASE_URL,
  timeoutMs = DEFAULT_TIMEOUT_MS,
  maxRetries = DEFAULT_MAX_RETRIES,
  retryBaseMs = DEFAULT_RETRY_BASE_MS,
  fetchImpl = fetch,
}) {
  const base = apiBaseUrl.replace(/\/+$/, "");
  const endpoint = `${base}/bot${botToken}/sendMessage`;
  const payload = JSON.stringify({
    chat_id: chatId,
    text,
    disable_web_page_preview: true,
  });

  let lastError = null;
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      const resp = await fetchImpl(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: payload,
        signal: AbortSignal.timeout(timeoutMs),
      });

      if (resp.ok) {
        return { ok: true, status: resp.status, attempts: attempt + 1 };
      }

      const errBody = await resp.text().catch(() => "");
      const sanitized = maskSecret(
        `telegram HTTP ${resp.status}: ${errBody.slice(0, 200)}`,
        botToken,
      );
      lastError = new Error(sanitized);

      // Do not retry non-429 4xx responses (e.g. bad chat_id / unauthorized)
      if (resp.status >= 400 && resp.status < 500 && resp.status !== 429) {
        break;
      }
    } catch (err) {
      lastError = new Error(maskSecret(err?.message || String(err), botToken));
    }

    if (attempt < maxRetries) {
      const backoffMs = retryBaseMs * 2 ** attempt;
      await sleep(backoffMs);
    }
  }

  throw lastError || new Error("telegram delivery failed");
}

function defaultLog(record) {
  process.stdout.write(`${JSON.stringify(record)}\n`);
}

function sendJSON(res, statusCode, body) {
  const data = JSON.stringify(body);
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": Buffer.byteLength(data),
  });
  res.end(data);
}

function readBody(req, maxBytes = MAX_BODY_BYTES) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let total = 0;
    let settled = false;

    req.on("data", (chunk) => {
      if (settled) return;
      total += chunk.length;
      if (total > maxBytes) {
        settled = true;
        const err = new Error("payload_too_large");
        err.code = "PAYLOAD_TOO_LARGE";
        reject(err);
        req.resume();
        return;
      }
      chunks.push(chunk);
    });

    req.on("end", () => {
      if (settled) return;
      settled = true;
      resolve(Buffer.concat(chunks).toString("utf8"));
    });

    req.on("error", (err) => {
      if (settled) return;
      settled = true;
      reject(err);
    });
  });
}

export function createRelayServer(options = {}) {
  const botToken = String(options.botToken ?? process.env.TELEGRAM_BOT_TOKEN ?? "").trim();
  const chatId = String(options.chatId ?? process.env.TELEGRAM_CHAT_ID ?? "").trim();
  const apiBaseUrl = String(
    options.apiBaseUrl ?? process.env.TELEGRAM_API_BASE_URL ?? DEFAULT_API_BASE_URL,
  ).trim();
  const timeoutMs = Number(options.timeoutMs ?? process.env.TELEGRAM_TIMEOUT_MS ?? DEFAULT_TIMEOUT_MS);
  const maxRetries = Number(
    options.maxRetries ?? process.env.TELEGRAM_MAX_RETRIES ?? DEFAULT_MAX_RETRIES,
  );
  const retryBaseMs = Number(
    options.retryBaseMs ?? process.env.TELEGRAM_RETRY_BASE_MS ?? DEFAULT_RETRY_BASE_MS,
  );
  const maxStoredAlerts = Number(options.maxStoredAlerts ?? MAX_STORED_ALERTS);
  const log = options.log ?? defaultLog;
  const fetchImpl = options.fetchImpl ?? fetch;

  const liveMode = isLiveTelegramConfigured(botToken, chatId);
  const mode = liveMode ? "live" : "mock";
  const store = [];

  const emitLog = (level, fields) => {
    const safeFields = {};
    for (const [k, v] of Object.entries(fields)) {
      safeFields[k] = typeof v === "string" ? maskSecret(v, botToken) : v;
    }
    log({
      time: new Date().toISOString(),
      level,
      service: "telegram-relay",
      mode,
      ...safeFields,
    });
  };

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url || "/", "http://127.0.0.1");

    if (req.method === "GET" && url.pathname === "/healthz") {
      return sendJSON(res, 200, {
        status: "ok",
        service: "telegram-relay",
        mode,
        stored_alerts: store.length,
      });
    }

    if (req.method === "GET" && url.pathname === "/alerts") {
      return sendJSON(res, 200, store);
    }

    if (req.method === "POST" && url.pathname === "/webhook") {
      let rawBody;
      try {
        rawBody = await readBody(req);
      } catch (err) {
        if (err?.code === "PAYLOAD_TOO_LARGE") {
          return sendJSON(res, 413, { error: "payload_too_large" });
        }
        emitLog("ERROR", { event: "read_body_failed", error: err?.message || String(err) });
        return sendJSON(res, 400, { error: "invalid_request_body" });
      }

      let payload;
      try {
        payload = rawBody ? JSON.parse(rawBody) : null;
      } catch {
        return sendJSON(res, 400, { error: "invalid_json" });
      }
      if (!payload || typeof payload !== "object") {
        return sendJSON(res, 400, { error: "json_object_required" });
      }

      const formatted = formatAlertmanagerPayload(payload);
      const record = {
        id: randomUUID(),
        received_at: new Date().toISOString(),
        mode,
        status: formatted.status,
        receiver: formatted.receiver,
        services: formatted.services,
        alertnames: formatted.alertnames,
        alert_count: formatted.alerts.length,
        alerts: formatted.alerts,
        formatted_text: formatted.text,
        delivery_status: liveMode ? "pending" : "mock_stored",
      };

      store.unshift(record);
      if (store.length > maxStoredAlerts) {
        store.length = maxStoredAlerts;
      }

      if (!liveMode) {
        emitLog("INFO", {
          event: "alert_received",
          alert_id: record.id,
          status: record.status,
          alert_count: record.alert_count,
          services: record.services,
          alertnames: record.alertnames,
        });
        return sendJSON(res, 200, {
          status: "accepted",
          mode,
          alert_id: record.id,
          alerts_processed: record.alert_count,
        });
      }

      try {
        const result = await sendTelegramMessage({
          botToken,
          chatId,
          text: formatted.text,
          apiBaseUrl,
          timeoutMs,
          maxRetries,
          retryBaseMs,
          fetchImpl,
        });
        record.delivery_status = "sent";
        emitLog("INFO", {
          event: "telegram_sent",
          alert_id: record.id,
          status: record.status,
          alert_count: record.alert_count,
          services: record.services,
          alertnames: record.alertnames,
          attempts: result.attempts,
        });
        return sendJSON(res, 200, {
          status: "sent",
          mode,
          alert_id: record.id,
          alerts_processed: record.alert_count,
          attempts: result.attempts,
        });
      } catch (err) {
        record.delivery_status = "failed";
        const safeErr = maskSecret(err?.message || String(err), botToken);
        emitLog("ERROR", {
          event: "telegram_send_failed",
          alert_id: record.id,
          status: record.status,
          services: record.services,
          alertnames: record.alertnames,
          error: safeErr,
        });
        return sendJSON(res, 502, {
          status: "delivery_failed",
          mode,
          alert_id: record.id,
          error: safeErr,
        });
      }
    }

    if (url.pathname === "/healthz" || url.pathname === "/alerts" || url.pathname === "/webhook") {
      return sendJSON(res, 405, { error: "method_not_allowed" });
    }

    return sendJSON(res, 404, { error: "not_found" });
  });

  return { server, store, mode, emitLog };
}

const isMain =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;

if (isMain) {
  const port = Number(process.env.PORT || DEFAULT_PORT);
  const { server, mode, emitLog } = createRelayServer();

  server.listen(port, "0.0.0.0", () => {
    emitLog("INFO", {
      event: "server_started",
      port,
      message: `telegram-relay listening on :${port} (${mode} mode)`,
    });
  });

  const shutdown = (signal) => {
    emitLog("INFO", { event: "server_stopping", signal });
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(1), 5000).unref();
  };

  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));
}
