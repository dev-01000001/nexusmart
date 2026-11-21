import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  OPEN_SITE_CHAT_EVENT,
  computeReconnectDelayMs,
  detectOffPlatformContact,
  mergeMessages,
  normalizeAutoReplyConfig,
  normalizeConversation,
  normalizeMessage,
  parseOrderRefCard,
  parseProductRefCard,
  parseWsIncomingEvent,
  validateAutoReplyInput,
  validateOutgoingMessage,
} from "./chatTypes.ts";
import { createChatSocketManager } from "./chatClient.ts";

describe("chatTypes normalizers & mergeMessages", () => {
  it("normalizes proto-style conversations and defaults missing fields", () => {
    const conv = normalizeConversation({
      id: "conv-1",
      buyer_id: "buyer-abcdef123",
      shop_id: "shop-99",
      site_host: "bach-dang.nexusmart.localhost",
      last_message_id: "msg-10",
      last_message_seq: 10,
      last_message_preview: "Áo này còn size L không shop?",
      last_message_sender_role: "buyer",
      last_message_at: "2026-11-21T08:00:00Z",
      unread_count: 3,
      created_at: "2026-11-21T07:00:00Z",
      updated_at: "2026-11-21T08:00:00Z",
    });

    assert.ok(conv);
    assert.equal(conv.id, "conv-1");
    assert.equal(conv.shop_id, "shop-99");
    assert.equal(conv.unread_count, 3);
    assert.equal(conv.buyer_name, "Khách hàng #buyer-");
    assert.equal(conv.last_message_preview, "Áo này còn size L không shop?");
    assert.equal(normalizeConversation({}), null);
    assert.equal(normalizeConversation(null), null);
  });

  it("normalizes messages across all kinds including proto enum strings", () => {
    const msg = normalizeMessage({
      id: "m-1",
      conversation_id: "conv-1",
      sender_id: "u-1",
      sender_role: "buyer",
      kind: "MESSAGE_KIND_PRODUCT",
      body: "Áo Polo Cotton Pique",
      ref_id: "prod-1",
      metadata_json: JSON.stringify({
        slug: "ao-polo",
        price_vnd: 249000,
        image: "/products/p.jpg",
      }),
      seq: 4,
      created_at: "2026-11-21T08:01:00Z",
    });

    assert.ok(msg);
    assert.equal(msg.kind, "product");
    assert.equal(msg.ref_id, "prod-1");
    assert.equal(msg.seq, 4);
    const card = parseProductRefCard(msg);
    assert.equal(card.slug, "ao-polo");
    assert.equal(card.priceVnd, 249000);
    assert.equal(card.image, "/products/p.jpg");

    const orderMsg = normalizeMessage({
      id: "m-2",
      conversation_id: "conv-1",
      sender_id: "u-1",
      kind: "order",
      body: "Đơn hàng NM-2026-88",
      ref_id: "ord-88",
      metadata: { order_code: "NM-2026-88" },
      created_at: "2026-11-21T08:02:00Z",
    });
    assert.ok(orderMsg);
    assert.equal(parseOrderRefCard(orderMsg).orderCode, "NM-2026-88");
  });

  it("merges messages, dedupes by id, reconciles client_msg_id, and sorts ascending", () => {
    const existing = [
      {
        id: "m-2",
        conversation_id: "c-1",
        sender_id: "s-1",
        kind: "text",
        body: "Second",
        ref_id: "",
        created_at: "2026-11-21T08:02:00Z",
      },
      {
        id: "cli-temp-1",
        client_msg_id: "cli-temp-1",
        conversation_id: "c-1",
        sender_id: "u-1",
        kind: "text",
        body: "Optimistic",
        ref_id: "",
        created_at: "2026-11-21T08:03:00Z",
      },
    ];

    const incoming = [
      {
        id: "m-1",
        conversation_id: "c-1",
        sender_id: "u-1",
        kind: "text",
        body: "First",
        ref_id: "",
        created_at: "2026-11-21T08:01:00Z",
      },
      {
        id: "m-3",
        client_msg_id: "cli-temp-1",
        conversation_id: "c-1",
        sender_id: "u-1",
        kind: "text",
        body: "Optimistic",
        ref_id: "",
        seq: 3,
        created_at: "2026-11-21T08:03:01Z",
      },
      {
        id: "m-2",
        conversation_id: "c-1",
        sender_id: "s-1",
        kind: "text",
        body: "Second updated",
        ref_id: "",
        created_at: "2026-11-21T08:02:00Z",
      },
    ];

    const merged = mergeMessages(existing, incoming);
    assert.equal(merged.length, 3);
    assert.deepEqual(
      merged.map((m) => m.id),
      ["m-1", "m-2", "m-3"]
    );
    assert.equal(merged[1].body, "Second updated");
  });
});

describe("validateOutgoingMessage & validateAutoReplyInput", () => {
  it("validates text, image, product, and order payloads strictly", () => {
    assert.equal(validateOutgoingMessage({ kind: "text", body: "Xin chào" }).ok, true);
    assert.equal(validateOutgoingMessage({ kind: "text", body: "   " }).ok, false);
    assert.equal(validateOutgoingMessage({ kind: "text", body: "a".repeat(2001) }).ok, false);
    assert.equal(validateOutgoingMessage({ kind: "script", body: "hi" }).ok, false);

    assert.equal(validateOutgoingMessage({ kind: "image", body: "/products/tee.jpg" }).ok, true);
    assert.equal(
      validateOutgoingMessage({ kind: "image", body: "https://cdn.nexusmart.vn/img/1.png" }).ok,
      true
    );
    for (const unsafe of [
      "javascript:alert(1)",
      "data:image/svg+xml,<svg/>",
      "http://evil.example/a.png",
      "//evil.example/a.png",
      "",
    ]) {
      assert.equal(
        validateOutgoingMessage({ kind: "image", body: unsafe }).ok,
        false,
        `expected unsafe image rejected: ${unsafe}`
      );
    }

    assert.equal(
      validateOutgoingMessage({ kind: "product", ref_id: "prod-1", body: "Áo" }).ok,
      true
    );
    assert.equal(validateOutgoingMessage({ kind: "product", ref_id: "", body: "Áo" }).ok, false);
    assert.equal(validateOutgoingMessage({ kind: "order", ref_id: "ord-9" }).ok, true);
    assert.equal(validateOutgoingMessage({ kind: "order", ref_id: "" }).ok, false);
  });

  it("validates and normalizes auto-reply configuration", () => {
    assert.equal(validateAutoReplyInput({ enabled: true, message: "" }).ok, false);
    assert.equal(validateAutoReplyInput({ enabled: false, message: "" }).ok, true);
    assert.equal(
      validateAutoReplyInput({ enabled: true, message: "Chào bạn, xưởng sẽ phản hồi sớm." }).ok,
      true
    );
    assert.equal(validateAutoReplyInput({ enabled: true, message: "x".repeat(501) }).ok, false);

    const cfg = normalizeAutoReplyConfig({
      config: { enabled: true, greeting_message: "Xin chào quý khách" },
    });
    assert.equal(cfg.enabled, true);
    assert.equal(cfg.message, "Xin chào quý khách");
  });
});

describe("detectOffPlatformContact (UC-B14) & reconnect backoff", () => {
  it("flags Vietnamese phone numbers and external URLs without false positives", () => {
    assert.deepEqual(detectOffPlatformContact("Gọi mình số 0912345678 nhé"), {
      flagged: true,
      reasons: ["phone"],
    });
    assert.deepEqual(detectOffPlatformContact("Liên hệ +84 987 654 321"), {
      flagged: true,
      reasons: ["phone"],
    });
    assert.deepEqual(detectOffPlatformContact("Nhắn qua https://zalo.me/abc nha"), {
      flagged: true,
      reasons: ["external_link"],
    });
    assert.deepEqual(
      detectOffPlatformContact("Gọi 0909.123.456 hoặc vào http://shopngoai.vn/mua"),
      {
        flagged: true,
        reasons: ["phone", "external_link"],
      }
    );

    // Safe messages must not trigger warnings
    assert.equal(detectOffPlatformContact("Áo giá 249.000₫, mã đơn NM-2026-000123").flagged, false);
    assert.equal(
      detectOffPlatformContact("Xem tại https://bach-dang.nexusmart.vn/san-pham/ao-polo").flagged,
      false
    );
    assert.equal(OPEN_SITE_CHAT_EVENT, "nexusmart:open-chat");
  });

  it("computes capped exponential backoff 1s -> 2s -> 4s -> 8s -> 15s", () => {
    assert.equal(computeReconnectDelayMs(-1), 1000);
    assert.equal(computeReconnectDelayMs(0), 1000);
    assert.equal(computeReconnectDelayMs(1), 2000);
    assert.equal(computeReconnectDelayMs(2), 4000);
    assert.equal(computeReconnectDelayMs(3), 8000);
    assert.equal(computeReconnectDelayMs(4), 15000);
    assert.equal(computeReconnectDelayMs(10), 15000);
  });

  it("parses incoming WebSocket frames safely", () => {
    const msgEv = parseWsIncomingEvent(
      JSON.stringify({
        type: "message",
        message: {
          id: "m-50",
          conversation_id: "c-1",
          sender_id: "seller-1",
          kind: "text",
          body: "Chào bạn",
          created_at: "2026-11-21T08:10:00Z",
        },
      })
    );
    assert.equal(msgEv?.type, "message");
    if (msgEv?.type === "message") {
      assert.equal(msgEv.message.body, "Chào bạn");
    }

    assert.equal(
      parseWsIncomingEvent(
        JSON.stringify({ type: "typing", conversation_id: "c-1", sender_id: "s" })
      )?.type,
      "typing"
    );
    assert.equal(
      parseWsIncomingEvent(
        JSON.stringify({ type: "read", conversation_id: "c-1", last_read_seq: 9 })
      )?.type,
      "read"
    );
    assert.equal(
      parseWsIncomingEvent(
        JSON.stringify({
          type: "ack",
          conversation_id: "c-1",
          message_id: "m-9",
          client_msg_id: "cli-1",
        })
      )?.type,
      "ack"
    );
    assert.equal(parseWsIncomingEvent("{not-json"), null);
  });
});

describe("createChatSocketManager", () => {
  it("requests ws-ticket, sends frames when open, reconnects with sync, and disposes cleanly", async () => {
    const createdSockets = [];
    class FakeWebSocket {
      constructor(url) {
        this.url = url;
        this.readyState = 0;
        this.sent = [];
        this.closed = false;
        this.onopen = null;
        this.onmessage = null;
        this.onerror = null;
        this.onclose = null;
        createdSockets.push(this);
      }
      open() {
        this.readyState = 1;
        this.onopen?.({});
      }
      receive(payload) {
        this.onmessage?.({ data: JSON.stringify(payload) });
      }
      drop() {
        this.readyState = 3;
        this.onclose?.({});
      }
      send(data) {
        this.sent.push(JSON.parse(data));
      }
      close() {
        this.closed = true;
        this.readyState = 3;
      }
    }

    const scheduled = [];
    const statuses = [];
    const received = [];
    let syncCount = 0;
    let ticketCalls = 0;

    const manager = createChatSocketManager({
      ticketEndpoint: "/api/chat/ws-ticket",
      onMessage: (ev) => received.push(ev),
      onStatusChange: (s) => statuses.push(s),
      onReconnectSync: () => {
        syncCount += 1;
      },
      fetchImpl: async (url) => {
        ticketCalls += 1;
        assert.equal(url, "/api/chat/ws-ticket");
        return new Response(
          JSON.stringify({
            ticket: `tk-${ticketCalls}`,
            ws_url: `ws://localhost:28080/api/v1/chat/ws?ticket=tk-${ticketCalls}`,
            expires_at: "2026-11-21T08:15:00Z",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      },
      WebSocketImpl: FakeWebSocket,
      setTimeoutImpl: (cb, ms) => {
        const entry = { cb, ms, cleared: false };
        scheduled.push(entry);
        return entry;
      },
      clearTimeoutImpl: (entry) => {
        if (entry) entry.cleared = true;
      },
    });

    // Before open, send() returns false so caller falls back to HTTP
    assert.equal(manager.send({ conversation_id: "c-1", kind: "text", body: "fallback" }), false);

    manager.connect();
    await new Promise((r) => setImmediate(r));
    assert.equal(createdSockets.length, 1);
    assert.match(createdSockets[0].url, /ticket=tk-1/);

    createdSockets[0].open();
    assert.equal(manager.getStatus(), "connected");
    assert.equal(syncCount, 0);

    // Send over open WebSocket returns true
    const sentOk = manager.send({
      conversation_id: "c-1",
      kind: "text",
      body: "Xin chào shop",
      client_msg_id: "cli-1",
    });
    assert.equal(sentOk, true);
    assert.equal(createdSockets[0].sent.length, 1);
    assert.equal(createdSockets[0].sent[0].type, "send");
    assert.equal(createdSockets[0].sent[0].body, "Xin chào shop");

    // Incoming message frame parsed and dispatched
    createdSockets[0].receive({
      type: "message",
      message: {
        id: "m-101",
        conversation_id: "c-1",
        sender_id: "seller-1",
        kind: "text",
        body: "Shop nghe ạ",
        created_at: "2026-11-21T08:10:00Z",
      },
    });
    assert.equal(received.length, 1);
    assert.equal(received[0].type, "message");

    // Unexpected close schedules reconnect at 1000ms and runs onReconnectSync on reopen
    createdSockets[0].drop();
    assert.equal(manager.getStatus(), "reconnecting");
    assert.equal(scheduled.length, 1);
    assert.equal(scheduled[0].ms, 1000);

    scheduled[0].cb();
    await new Promise((r) => setImmediate(r));
    assert.equal(createdSockets.length, 2);
    createdSockets[1].open();
    assert.equal(manager.getStatus(), "connected");
    assert.equal(syncCount, 1);

    // Dispose closes socket and stops further events
    manager.dispose();
    assert.equal(createdSockets[1].closed, true);
    assert.equal(manager.getStatus(), "offline");
    assert.ok(statuses.includes("connecting"));
    assert.ok(statuses.includes("connected"));
    assert.ok(statuses.includes("reconnecting"));
  });

  it("stops retrying on 401 unauthorized ticket response", async () => {
    const statuses = [];
    const scheduled = [];
    const manager = createChatSocketManager({
      ticketEndpoint: "/api/chat/ws-ticket",
      onMessage: () => {},
      onStatusChange: (s) => statuses.push(s),
      fetchImpl: async () =>
        new Response(JSON.stringify({ error: { code: "UNAUTHENTICATED" } }), { status: 401 }),
      WebSocketImpl: class {},
      setTimeoutImpl: (cb, ms) => {
        scheduled.push({ cb, ms });
        return 1;
      },
      clearTimeoutImpl: () => {},
    });

    manager.connect();
    await new Promise((r) => setImmediate(r));
    assert.equal(manager.getStatus(), "offline");
    assert.equal(scheduled.length, 0);
    assert.deepEqual(statuses, ["connecting", "offline"]);
  });
});
