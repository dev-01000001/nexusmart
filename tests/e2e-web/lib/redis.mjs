import net from "node:net";
import { config } from "./config.mjs";

function encodeRespCommand(args) {
  const parts = [`*${args.length}\r\n`];
  for (const arg of args) {
    const buf = Buffer.from(String(arg), "utf8");
    parts.push(`$${buf.length}\r\n`, buf, "\r\n");
  }
  return Buffer.concat(parts.map((p) => (Buffer.isBuffer(p) ? p : Buffer.from(p, "utf8"))));
}

/**
 * Attempts to decode one complete RESP2 value from `buf` starting at `offset`.
 * Returns `{ value, nextOffset }` when complete, or `null` if more bytes are needed.
 */
function tryDecodeResp(buf, offset = 0) {
  if (offset >= buf.length) return null;
  const crlf = buf.indexOf("\r\n", offset);
  if (crlf === -1) return null;

  const prefix = String.fromCharCode(buf[offset]);
  const line = buf.subarray(offset + 1, crlf).toString("utf8");
  let pos = crlf + 2;

  if (prefix === "+") return { value: line, nextOffset: pos };
  if (prefix === "-") return { value: new Error(`Redis error: ${line}`), isError: true, nextOffset: pos };
  if (prefix === ":") return { value: Number.parseInt(line, 10), nextOffset: pos };
  if (prefix === "$") {
    const len = Number.parseInt(line, 10);
    if (len < 0) return { value: null, nextOffset: pos };
    if (pos + len + 2 > buf.length) return null;
    const str = buf.subarray(pos, pos + len).toString("utf8");
    return { value: str, nextOffset: pos + len + 2 };
  }
  if (prefix === "*") {
    const count = Number.parseInt(line, 10);
    if (count < 0) return { value: null, nextOffset: pos };
    const items = [];
    for (let i = 0; i < count; i += 1) {
      const parsed = tryDecodeResp(buf, pos);
      if (!parsed) return null;
      if (parsed.isError) return parsed;
      items.push(parsed.value);
      pos = parsed.nextOffset;
    }
    return { value: items, nextOffset: pos };
  }
  throw new Error(`Unsupported RESP prefix "${prefix}"`);
}

function parseHostPort(addr) {
  const trimmed = String(addr ?? "").trim();
  if (!trimmed) return null;
  const idx = trimmed.lastIndexOf(":");
  if (idx <= 0) return { host: trimmed, port: 6379 };
  const host = trimmed.slice(0, idx).replace(/^\[|\]$/g, "");
  const port = Number.parseInt(trimmed.slice(idx + 1), 10) || 6379;
  return { host, port };
}

/**
 * Deletes Redis gateway rate-limit keys (`rl:*`) over raw TCP RESP2 (zero external deps)
 * so repeated `make e2e-web` runs stay deterministic without weakening default rate limits.
 */
export async function resetRateLimitKeys(pattern = "rl:*", { timeoutMs = 5000 } = {}) {
  const target = parseHostPort(config.redisAddr);
  if (!target) return 0;

  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: target.host, port: target.port });
    let buffer = Buffer.alloc(0);
    let settled = false;
    let pendingResolve = null;
    let pendingReject = null;

    const finish = (err, value) => {
      if (settled) return;
      settled = true;
      socket.destroy();
      if (err) reject(err);
      else resolve(value);
    };

    socket.setTimeout(timeoutMs, () => {
      finish(new Error(`Redis ${config.redisAddr} timed out after ${timeoutMs}ms`));
    });
    socket.on("error", (err) => {
      if (pendingReject) {
        const rej = pendingReject;
        pendingResolve = null;
        pendingReject = null;
        rej(err);
      }
      finish(err);
    });
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      while (pendingResolve) {
        let parsed;
        try {
          parsed = tryDecodeResp(buffer, 0);
        } catch (err) {
          const rej = pendingReject;
          pendingResolve = null;
          pendingReject = null;
          rej(err);
          return;
        }
        if (!parsed) return;
        buffer = buffer.subarray(parsed.nextOffset);
        const res = pendingResolve;
        const rej = pendingReject;
        pendingResolve = null;
        pendingReject = null;
        if (parsed.isError) rej(parsed.value);
        else res(parsed.value);
      }
    });

    const send = (args) =>
      new Promise((res, rej) => {
        pendingResolve = res;
        pendingReject = rej;
        socket.write(encodeRespCommand(args));
      });

    socket.once("connect", async () => {
      try {
        let cursor = "0";
        let deleted = 0;
        do {
          const reply = await send(["SCAN", cursor, "MATCH", pattern, "COUNT", "200"]);
          if (!Array.isArray(reply) || reply.length !== 2) {
            throw new Error(`Unexpected Redis SCAN reply: ${JSON.stringify(reply)}`);
          }
          cursor = String(reply[0]);
          const keys = Array.isArray(reply[1]) ? reply[1].filter(Boolean) : [];
          if (keys.length > 0) {
            const count = await send(["DEL", ...keys]);
            if (typeof count === "number") deleted += count;
          }
        } while (cursor !== "0");
        finish(null, deleted);
      } catch (err) {
        finish(err);
      }
    });
  });
}
