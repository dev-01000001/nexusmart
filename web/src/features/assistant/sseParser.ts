// Pure Server-Sent Events parser for the NexusMart shopping assistant stream.
// Zero DOM or Next.js imports so it runs identically in browsers and Node 22 unit tests.

export type AssistantSseEvent =
  | {
      type: "token";
      text: string;
    }
  | {
      type: "tool_call";
      id?: string;
      name: string;
      arguments?: Record<string, unknown>;
    }
  | {
      type: "tool_result";
      id?: string;
      name: string;
      ok: boolean;
      requiresAuth?: boolean;
      requiresConfirmation?: boolean;
      error?: string;
      data?: Record<string, unknown>;
      raw?: unknown;
    }
  | {
      type: "done";
      conversationId?: string;
      messageId?: string;
      usage?: Record<string, unknown>;
      raw?: unknown;
    }
  | {
      type: "error";
      code?: string;
      message: string;
    };

export interface SseParser {
  feed(chunk: string): void;
  flush(): void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pickString(obj: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === "string") return val;
  }
  return undefined;
}

function tryParseJson(raw: string): { ok: true; value: unknown } | { ok: false } {
  const trimmed = raw.trim();
  if (!trimmed) return { ok: false };
  try {
    return { ok: true, value: JSON.parse(trimmed) };
  } catch {
    return { ok: false };
  }
}

function normalizeEventType(rawEventName: string, parsed: unknown): string {
  const explicit = rawEventName.trim().toLowerCase();
  if (explicit && explicit !== "message") return explicit;
  if (isRecord(parsed)) {
    const fromBody = pickString(parsed, "type", "event");
    if (fromBody) return fromBody.trim().toLowerCase();
  }
  return "token";
}

function toAssistantEvent(rawEventName: string, rawData: string): AssistantSseEvent | null {
  const parsedRes = tryParseJson(rawData);
  const parsed = parsedRes.ok ? parsedRes.value : undefined;
  const kind = normalizeEventType(rawEventName, parsed);

  if (kind === "token" || kind === "delta" || kind === "chunk") {
    if (parsedRes.ok) {
      if (typeof parsed === "string") {
        return parsed.length > 0 ? { type: "token", text: parsed } : null;
      }
      if (isRecord(parsed)) {
        const text = pickString(parsed, "text", "delta", "token", "content");
        if (typeof text === "string" && text.length > 0) {
          return { type: "token", text };
        }
        return null;
      }
    }
    return rawData.length > 0 ? { type: "token", text: rawData } : null;
  }

  if (kind === "tool_call") {
    const obj = isRecord(parsed) ? parsed : {};
    const nested = isRecord(obj.tool_call) ? obj.tool_call : obj;
    const name = pickString(nested, "name", "tool", "tool_name") ?? "";
    if (!name) return null;
    const id = pickString(nested, "id", "tool_call_id");
    const rawArgs = nested.arguments ?? nested.args ?? nested.input;
    let args: Record<string, unknown> | undefined;
    if (isRecord(rawArgs)) {
      args = rawArgs;
    } else if (typeof rawArgs === "string") {
      const parsedArgs = tryParseJson(rawArgs);
      if (parsedArgs.ok && isRecord(parsedArgs.value)) {
        args = parsedArgs.value;
      }
    }
    return { type: "tool_call", id, name, arguments: args };
  }

  if (kind === "tool_result") {
    const obj = isRecord(parsed) ? parsed : {};
    const nested = isRecord(obj.tool_result) ? obj.tool_result : obj;
    const name = pickString(nested, "name", "tool", "tool_name") ?? "";
    const id = pickString(nested, "id", "tool_call_id");
    const error = pickString(nested, "error", "error_message");
    const ok = typeof nested.ok === "boolean" ? nested.ok : !error;
    const requiresAuth = Boolean(nested.requires_auth ?? nested.requiresAuth);
    const requiresConfirmation = Boolean(
      nested.requires_confirmation ?? nested.requiresConfirmation
    );
    const dataCandidate = nested.data ?? nested.result ?? nested.payload;
    const data = isRecord(dataCandidate) ? dataCandidate : isRecord(nested) ? nested : undefined;
    return {
      type: "tool_result",
      id,
      name,
      ok,
      requiresAuth,
      requiresConfirmation,
      error,
      data,
      raw: parsedRes.ok ? parsed : rawData,
    };
  }

  if (kind === "done" || kind === "complete" || kind === "end") {
    const obj = isRecord(parsed) ? parsed : {};
    const conversationId = pickString(obj, "conversation_id", "conversationId");
    const messageId = pickString(obj, "message_id", "messageId");
    const usage = isRecord(obj.usage) ? obj.usage : undefined;
    return {
      type: "done",
      conversationId,
      messageId,
      usage,
      raw: parsedRes.ok ? parsed : rawData || undefined,
    };
  }

  if (kind === "error") {
    if (isRecord(parsed)) {
      const nestedErr = isRecord(parsed.error) ? parsed.error : parsed;
      const code = pickString(nestedErr, "code") ?? pickString(parsed, "code");
      const message =
        pickString(nestedErr, "message") ??
        pickString(parsed, "message", "error") ??
        "Trợ lý tạm thời không thể phản hồi lúc này.";
      return { type: "error", code, message };
    }
    return {
      type: "error",
      message: rawData.trim() || "Trợ lý tạm thời không thể phản hồi lúc này.",
    };
  }

  // Unknown event type with plain text or text field falls back gracefully to token.
  if (isRecord(parsed)) {
    const text = pickString(parsed, "text", "content", "delta");
    if (text) return { type: "token", text };
  }
  return null;
}

/**
 * Create a streaming SSE parser that handles arbitrary chunk splits, `\r\n` and `\n` line endings,
 * multi-line `data:` concatenation, JSON payloads, and plain-text token fallbacks.
 */
export function createSseParser(onEvent: (ev: AssistantSseEvent) => void): SseParser {
  let buffer = "";
  let pendingCr = false;
  let eventName = "";
  let dataLines: string[] = [];

  const dispatchCurrent = () => {
    if (!eventName && dataLines.length === 0) return;
    const rawData = dataLines.join("\n");
    const ev = toAssistantEvent(eventName, rawData);
    eventName = "";
    dataLines = [];
    if (ev) onEvent(ev);
  };

  const processLine = (line: string) => {
    if (line === "") {
      dispatchCurrent();
      return;
    }
    if (line.startsWith(":")) {
      // SSE comment / heartbeat line
      return;
    }
    const colonIdx = line.indexOf(":");
    let field: string;
    let value: string;
    if (colonIdx === -1) {
      field = line;
      value = "";
    } else {
      field = line.slice(0, colonIdx);
      value = line.slice(colonIdx + 1);
      if (value.startsWith(" ")) {
        value = value.slice(1);
      }
    }

    if (field === "event") {
      eventName = value;
    } else if (field === "data") {
      dataLines.push(value);
    }
  };

  return {
    feed(chunk: string) {
      if (!chunk) return;
      let text = chunk;
      // Handle a `\r\n` pair split across two adjacent chunks.
      if (pendingCr) {
        pendingCr = false;
        if (text.startsWith("\n")) {
          text = text.slice(1);
        }
      }

      for (let i = 0; i < text.length; i++) {
        const ch = text[i];
        if (ch === "\n") {
          processLine(buffer);
          buffer = "";
        } else if (ch === "\r") {
          processLine(buffer);
          buffer = "";
          if (i + 1 < text.length) {
            if (text[i + 1] === "\n") {
              i++;
            }
          } else {
            pendingCr = true;
          }
        } else {
          buffer += ch;
        }
      }
    },

    flush() {
      pendingCr = false;
      if (buffer.length > 0) {
        processLine(buffer);
        buffer = "";
      }
      dispatchCurrent();
    },
  };
}
