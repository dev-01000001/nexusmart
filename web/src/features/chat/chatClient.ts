// Browser-side WebSocket connection manager for buyer & seller chat.
// Requests a short-lived ticket via same-origin BFF, opens WebSocket, parses frames,
// reconnects with capped exponential backoff, and triggers cursor catch-up sync.
import {
  computeReconnectDelayMs,
  parseWsIncomingEvent,
  type ChatConnectionStatus,
  type ChatMessageKind,
  type WsIncomingEvent,
} from "./chatTypes.ts";

export interface WebSocketLike {
  readonly readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

export type WebSocketConstructor = new (url: string) => WebSocketLike;

export interface ChatSocketSendPayload {
  conversation_id: string;
  kind: ChatMessageKind;
  body: string;
  ref_id?: string;
  client_msg_id?: string;
  metadata?: Record<string, unknown>;
}

export interface ChatSocketManagerOptions {
  ticketEndpoint: string;
  onMessage: (event: WsIncomingEvent) => void;
  onStatusChange?: (status: ChatConnectionStatus) => void;
  onReconnectSync?: () => void | Promise<void>;
  /** Optional dependency overrides for deterministic unit tests. */
  fetchImpl?: typeof fetch;
  WebSocketImpl?: WebSocketConstructor;
  setTimeoutImpl?: (cb: () => void, ms: number) => ReturnType<typeof setTimeout>;
  clearTimeoutImpl?: (id: ReturnType<typeof setTimeout>) => void;
  maxConsecutiveFailures?: number;
}

export interface ChatSocketManager {
  connect(): void;
  send(payload: ChatSocketSendPayload): boolean;
  sendRead(conversationId: string, lastReadSeq?: number, messageId?: string): boolean;
  sendTyping(conversationId: string, isTyping: boolean): boolean;
  getStatus(): ChatConnectionStatus;
  dispose(): void;
}

const WS_OPEN = 1;

export function createChatSocketManager(options: ChatSocketManagerOptions): ChatSocketManager {
  const fetchFn = options.fetchImpl ?? ((...args) => globalThis.fetch(...args));
  const setTimer = options.setTimeoutImpl ?? ((cb, ms) => globalThis.setTimeout(cb, ms));
  const clearTimer = options.clearTimeoutImpl ?? ((id) => globalThis.clearTimeout(id));
  const maxFailures = Math.max(1, options.maxConsecutiveFailures ?? 8);

  let status: ChatConnectionStatus = "idle";
  let socket: WebSocketLike | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let ticketAbort: AbortController | null = null;
  let attempt = 0;
  let hasConnectedOnce = false;
  let disposed = false;
  let connecting = false;

  const setStatus = (next: ChatConnectionStatus) => {
    if (status === next) return;
    status = next;
    options.onStatusChange?.(next);
  };

  const clearReconnectTimer = () => {
    if (reconnectTimer !== null) {
      clearTimer(reconnectTimer);
      reconnectTimer = null;
    }
  };

  const resolveWsConstructor = (): WebSocketConstructor | null => {
    if (options.WebSocketImpl) return options.WebSocketImpl;
    if (typeof globalThis.WebSocket === "function") {
      return globalThis.WebSocket as unknown as WebSocketConstructor;
    }
    return null;
  };

  const scheduleReconnect = () => {
    if (disposed) return;
    if (attempt >= maxFailures && !hasConnectedOnce) {
      setStatus("offline");
      return;
    }
    clearReconnectTimer();
    const delay = computeReconnectDelayMs(attempt);
    attempt += 1;
    setStatus("reconnecting");
    reconnectTimer = setTimer(() => {
      reconnectTimer = null;
      if (!disposed) {
        void openConnection();
      }
    }, delay);
  };

  const detachSocket = (ws: WebSocketLike | null) => {
    if (!ws) return;
    ws.onopen = null;
    ws.onmessage = null;
    ws.onerror = null;
    ws.onclose = null;
  };

  const openConnection = async () => {
    if (disposed || connecting) return;
    if (socket && socket.readyState === WS_OPEN) return;

    const WsCtor = resolveWsConstructor();
    if (!WsCtor) {
      setStatus("offline");
      return;
    }

    connecting = true;
    clearReconnectTimer();
    setStatus(hasConnectedOnce ? "reconnecting" : "connecting");

    ticketAbort?.abort();
    ticketAbort = new AbortController();

    let wsUrl = "";
    try {
      const res = await fetchFn(options.ticketEndpoint, {
        method: "POST",
        headers: { Accept: "application/json" },
        credentials: "same-origin",
        cache: "no-store",
        signal: ticketAbort.signal,
      });

      if (disposed) {
        connecting = false;
        return;
      }

      if (res.status === 401 || res.status === 403) {
        connecting = false;
        setStatus("offline");
        return;
      }

      if (!res.ok) {
        connecting = false;
        scheduleReconnect();
        return;
      }

      const data = (await res.json().catch(() => null)) as { ws_url?: string } | null;
      wsUrl = typeof data?.ws_url === "string" ? data.ws_url.trim() : "";
      if (!wsUrl) {
        connecting = false;
        scheduleReconnect();
        return;
      }
    } catch {
      connecting = false;
      if (!disposed) scheduleReconnect();
      return;
    }

    if (disposed) {
      connecting = false;
      return;
    }

    let ws: WebSocketLike;
    try {
      ws = new WsCtor(wsUrl);
    } catch {
      connecting = false;
      scheduleReconnect();
      return;
    }

    socket = ws;

    ws.onopen = () => {
      connecting = false;
      if (disposed || socket !== ws) return;
      const isReconnect = hasConnectedOnce;
      hasConnectedOnce = true;
      attempt = 0;
      setStatus("connected");
      if (isReconnect) {
        void options.onReconnectSync?.();
      }
    };

    ws.onmessage = (ev) => {
      if (disposed || socket !== ws) return;
      const parsed = parseWsIncomingEvent(ev.data);
      if (parsed) {
        options.onMessage(parsed);
      }
    };

    ws.onerror = () => {
      // Browser fires onclose immediately after onerror; reconnect happens in onclose.
    };

    ws.onclose = () => {
      connecting = false;
      if (socket === ws) {
        detachSocket(ws);
        socket = null;
      }
      if (!disposed) {
        scheduleReconnect();
      }
    };
  };

  const sendRaw = (frame: Record<string, unknown>): boolean => {
    if (disposed || !socket || socket.readyState !== WS_OPEN) return false;
    try {
      socket.send(JSON.stringify(frame));
      return true;
    } catch {
      return false;
    }
  };

  return {
    connect() {
      if (disposed) return;
      attempt = 0;
      void openConnection();
    },

    send(payload) {
      return sendRaw({
        type: "send",
        conversation_id: payload.conversation_id,
        kind: payload.kind,
        body: payload.body,
        ref_id: payload.ref_id ?? "",
        ...(payload.client_msg_id ? { client_msg_id: payload.client_msg_id } : {}),
        ...(payload.metadata ? { metadata_json: JSON.stringify(payload.metadata) } : {}),
      });
    },

    sendRead(conversationId, lastReadSeq, messageId) {
      return sendRaw({
        type: "read",
        conversation_id: conversationId,
        ...(lastReadSeq ? { last_read_seq: lastReadSeq } : {}),
        ...(messageId ? { message_id: messageId } : {}),
      });
    },

    sendTyping(conversationId, isTyping) {
      return sendRaw({
        type: "typing",
        conversation_id: conversationId,
        is_typing: isTyping,
      });
    },

    getStatus() {
      return status;
    },

    dispose() {
      disposed = true;
      connecting = false;
      clearReconnectTimer();
      ticketAbort?.abort();
      ticketAbort = null;
      if (socket) {
        const ws = socket;
        socket = null;
        detachSocket(ws);
        try {
          ws.close(1000, "client_dispose");
        } catch {
          // ignore close errors during teardown
        }
      }
      setStatus("offline");
    },
  };
}
