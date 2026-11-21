// Lightweight W3C Trace Context (traceparent/tracestate) and log PII/secret redaction helpers.
// Zero Node-only imports so this runs identically in Edge middleware, Node BFF, and Node 22 tests.

const TRACEPARENT_RE = /^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/;
const ZERO_TRACE_ID = "00000000000000000000000000000000";
const ZERO_SPAN_ID = "0000000000000000";

const SENSITIVE_KEY_RE =
  /(password|passwd|secret|token|authorization|cookie|api[_-]?key|email|phone|recipient_name|full_name|address|bank_account|account_number|cccd|id_card|tax_code)/i;

const BEARER_RE = /\bBearer\s+[A-Za-z0-9\-._~+/]+=*/gi;
const JWT_RE = /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g;
const DSN_CRED_RE = /([a-z][a-z0-9+.-]*:\/\/[^/\s:@]+:)[^/\s@]+(@)/gi;
const EMAIL_RE = /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi;
const VN_PHONE_RE =
  /(?<![0-9a-fA-F])(?:\+84|84|0)[\s.-]?(?:3|5|7|8|9)(?:[\s.-]?\d){8}(?![0-9a-fA-F])/g;

export interface TraceContext {
  traceparent: string;
  tracestate?: string;
  traceId: string;
}

function randomHex(byteLen: number): string {
  const bytes = new Uint8Array(byteLen);
  crypto.getRandomValues(bytes);
  let out = "";
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, "0"); // taste-skill-ignore: hex byte formatting
  }
  return out;
}

export function isValidTraceparent(raw: string | null | undefined): boolean {
  if (!raw) return false;
  const m = TRACEPARENT_RE.exec(raw.trim().toLowerCase());
  if (!m) return false;
  return m[1] !== ZERO_TRACE_ID && m[2] !== ZERO_SPAN_ID;
}

export function extractTraceId(traceparent: string | null | undefined): string {
  if (!traceparent) return "";
  const m = TRACEPARENT_RE.exec(traceparent.trim().toLowerCase());
  if (!m || m[1] === ZERO_TRACE_ID) return "";
  return m[1];
}

export function generateTraceparent(): string {
  const traceId = randomHex(16);
  const spanId = randomHex(8);
  return `00-${traceId}-${spanId}-01`;
}

export function resolveTraceContext(headers?: Pick<Headers, "get"> | null): TraceContext {
  const incoming = headers?.get("traceparent")?.trim().toLowerCase();
  const traceparent = isValidTraceparent(incoming) ? (incoming as string) : generateTraceparent();
  const tracestate = headers?.get("tracestate")?.trim();
  return {
    traceparent,
    ...(tracestate ? { tracestate: tracestate.slice(0, 512) } : {}),
    traceId: extractTraceId(traceparent),
  };
}

export function scrubLogString(input: string): string {
  return input
    .replace(BEARER_RE, "Bearer [REDACTED]")
    .replace(JWT_RE, "[REDACTED_JWT]")
    .replace(DSN_CRED_RE, "$1[REDACTED]$2")
    .replace(EMAIL_RE, "[REDACTED_EMAIL]")
    .replace(VN_PHONE_RE, "[REDACTED_PHONE]");
}

function redactValue(value: unknown, depth: number): unknown {
  if (depth > 4) return "[TRUNCATED]";
  if (typeof value === "string") return scrubLogString(value);
  if (value === null || typeof value !== "object") return value;
  if (Array.isArray(value)) {
    return value.slice(0, 20).map((item) => redactValue(item, depth + 1));
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    if (SENSITIVE_KEY_RE.test(k)) {
      out[k] = "[REDACTED]";
    } else {
      out[k] = redactValue(v, depth + 1);
    }
  }
  return out;
}

export function redactLogFields(fields: Record<string, unknown>): Record<string, unknown> {
  return redactValue(fields, 0) as Record<string, unknown>;
}
