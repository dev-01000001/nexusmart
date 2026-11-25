import { describe, it } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";

import {
  createRelayServer,
  formatAlertmanagerPayload,
  isLiveTelegramConfigured,
  maskSecret,
} from "./server.mjs";

function startServer(server) {
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${addr.port}`);
    });
  });
}

function stopServer(server) {
  return new Promise((resolve) => server.close(resolve));
}

const SAMPLE_ALERTMANAGER_PAYLOAD = {
  receiver: "telegram-relay",
  status: "firing",
  alerts: [
    {
      status: "firing",
      labels: {
        alertname: "NexusMartSLOFastBurn",
        service: "payment",
        severity: "critical",
      },
      annotations: {
        summary: "Fast SLO error budget burn on payment",
        description: "Error ratio exceeded 14.4x burn rate across 5m and 1h.",
      },
      startsAt: "2026-11-21T07:50:00Z",
    },
    {
      status: "firing",
      labels: {
        alertname: "NexusMartHighLatencyP95",
        service: "payment",
        severity: "warning",
      },
      annotations: {
        summary: "High p95 latency on payment",
        description: "Current p95 latency is 0.850s (>0.5s).",
      },
      startsAt: "2026-11-21T07:51:00Z",
    },
  ],
};

describe("telegram-relay unit tests", () => {
  it("detects mock vs live Telegram configuration accurately", () => {
    assert.equal(isLiveTelegramConfigured("", ""), false);
    assert.equal(isLiveTelegramConfigured("mock", "123456"), false);
    assert.equal(isLiveTelegramConfigured("changeme", "-1001234567890"), false);
    assert.equal(isLiveTelegramConfigured("123456:ABCDEF_valid_token", "placeholder"), false);
    assert.equal(isLiveTelegramConfigured("<replace-me>", "-100999"), false);
    assert.equal(
      isLiveTelegramConfigured("123456789:AAH_realLookingTokenValue", "-1001234567890"),
      true,
    );
  });

  it("formats Alertmanager webhook and direct alert payloads cleanly", () => {
    const formatted = formatAlertmanagerPayload(SAMPLE_ALERTMANAGER_PAYLOAD);
    assert.equal(formatted.status, "firing");
    assert.deepEqual(formatted.services, ["payment"]);
    assert.deepEqual(formatted.alertnames, [
      "NexusMartSLOFastBurn",
      "NexusMartHighLatencyP95",
    ]);
    assert.match(formatted.text, /\[FIRING\] NexusMart Alert \(2\)/);
    assert.match(formatted.text, /Service: payment/);
    assert.match(formatted.text, /Fast SLO error budget burn on payment/);

    const direct = formatAlertmanagerPayload({
      alertname: "ManualTestAlert",
      service: "order",
      severity: "warning",
      summary: "Order queue backed up",
    });
    assert.equal(direct.status, "firing");
    assert.deepEqual(direct.services, ["order"]);
    assert.match(direct.text, /ManualTestAlert/);
    assert.match(direct.text, /Order queue backed up/);
  });

  it("masks Telegram bot tokens in URLs and error strings", () => {
    const token = "987654321:AAH_superSecretBotTokenValue123";
    const raw = `request failed for https://api.telegram.org/bot${token}/sendMessage token=${token}`;
    const masked = maskSecret(raw, token);

    assert.equal(masked.includes(token), false);
    assert.equal(masked.includes("AAH_superSecret"), false);
    assert.match(masked, /\/bot\[REDACTED\]\/sendMessage/);
  });

  it("receives, logs, and lists alerts in mock mode with bounded buffer", async () => {
    const logs = [];
    const { server, mode } = createRelayServer({
      botToken: "",
      chatId: "",
      maxStoredAlerts: 3,
      log: (entry) => logs.push(entry),
    });
    assert.equal(mode, "mock");
    const baseUrl = await startServer(server);

    try {
      const healthRes = await fetch(`${baseUrl}/healthz`);
      assert.equal(healthRes.status, 200);
      const health = await healthRes.json();
      assert.equal(health.status, "ok");
      assert.equal(health.mode, "mock");
      assert.equal(health.stored_alerts, 0);

      // Invalid JSON returns 400
      const badRes = await fetch(`${baseUrl}/webhook`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{not-json",
      });
      assert.equal(badRes.status, 400);

      // Send 4 alerts and confirm ring buffer retains latest 3
      for (let i = 1; i <= 4; i++) {
        const res = await fetch(`${baseUrl}/webhook`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            ...SAMPLE_ALERTMANAGER_PAYLOAD,
            alerts: [
              {
                ...SAMPLE_ALERTMANAGER_PAYLOAD.alerts[0],
                annotations: { summary: `alert-${i}` },
              },
            ],
          }),
        });
        assert.equal(res.status, 200);
        const body = await res.json();
        assert.equal(body.mode, "mock");
        assert.equal(body.alerts_processed, 1);
      }

      const listRes = await fetch(`${baseUrl}/alerts`);
      assert.equal(listRes.status, 200);
      const alerts = await listRes.json();
      assert.equal(Array.isArray(alerts), true);
      assert.equal(alerts.length, 3);
      assert.equal(alerts[0].alerts[0].annotations.summary, "alert-4");
      assert.equal(alerts[2].alerts[0].annotations.summary, "alert-2");

      assert.equal(logs.length, 4);
      assert.equal(logs[0].service, "telegram-relay");
      assert.equal(logs[0].mode, "mock");
      assert.equal(logs[0].level, "INFO");
    } finally {
      await stopServer(server);
    }
  });

  it("dispatches to Telegram API with retry in live mode and never logs the token", async () => {
    const secretToken = "555111222:AAH_topSecretLiveTestTokenXYZ";
    const chatId = "-1009876543210";
    const telegramRequests = [];
    let attemptCount = 0;

    // Local mock Telegram Bot API server: fails first attempt with 500, succeeds on retry
    const mockTelegram = http.createServer((req, res) => {
      const chunks = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        attemptCount += 1;
        const bodyText = Buffer.concat(chunks).toString("utf8");
        telegramRequests.push({
          method: req.method,
          url: req.url,
          body: JSON.parse(bodyText),
        });
        if (attemptCount === 1) {
          res.writeHead(500, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ ok: false, description: "temporary upstream error" }));
          return;
        }
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ ok: true, result: { message_id: 42 } }));
      });
    });

    const telegramBaseUrl = await startServer(mockTelegram);
    const logs = [];
    const { server, mode } = createRelayServer({
      botToken: secretToken,
      chatId,
      apiBaseUrl: telegramBaseUrl,
      timeoutMs: 2000,
      maxRetries: 2,
      retryBaseMs: 20,
      log: (entry) => logs.push(entry),
    });
    assert.equal(mode, "live");
    const relayUrl = await startServer(server);

    try {
      const res = await fetch(`${relayUrl}/webhook`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(SAMPLE_ALERTMANAGER_PAYLOAD),
      });
      assert.equal(res.status, 200);
      const body = await res.json();
      assert.equal(body.status, "sent");
      assert.equal(body.mode, "live");
      assert.equal(body.attempts, 2);

      assert.equal(telegramRequests.length, 2);
      assert.equal(telegramRequests[0].url, `/bot${secretToken}/sendMessage`);
      assert.equal(telegramRequests[0].body.chat_id, chatId);
      assert.match(telegramRequests[0].body.text, /NexusMartSLOFastBurn/);

      // Ensure bot token never appears anywhere in logs or /alerts response
      const serializedLogs = JSON.stringify(logs);
      assert.equal(serializedLogs.includes(secretToken), false);

      const alertsRes = await fetch(`${relayUrl}/alerts`);
      const alertsText = await alertsRes.text();
      assert.equal(alertsText.includes(secretToken), false);
    } finally {
      await stopServer(server);
      await stopServer(mockTelegram);
    }
  });

  it("masks bot token even when live Telegram delivery fails permanently", async () => {
    const secretToken = "777888999:AAH_neverLeakThisToken999";
    const failingTelegram = http.createServer((_req, res) => {
      res.writeHead(401, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ ok: false, description: `Unauthorized for /bot${secretToken}` }));
    });
    const telegramBaseUrl = await startServer(failingTelegram);
    const logs = [];
    const { server } = createRelayServer({
      botToken: secretToken,
      chatId: "-100111222",
      apiBaseUrl: telegramBaseUrl,
      timeoutMs: 1000,
      maxRetries: 1,
      retryBaseMs: 10,
      log: (entry) => logs.push(entry),
    });
    const relayUrl = await startServer(server);

    try {
      const res = await fetch(`${relayUrl}/webhook`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(SAMPLE_ALERTMANAGER_PAYLOAD),
      });
      assert.equal(res.status, 502);
      const bodyText = await res.text();
      assert.equal(bodyText.includes(secretToken), false);
      assert.equal(JSON.stringify(logs).includes(secretToken), false);
    } finally {
      await stopServer(server);
      await stopServer(failingTelegram);
    }
  });
});
