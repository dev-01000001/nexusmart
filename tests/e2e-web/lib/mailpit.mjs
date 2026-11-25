import { config } from "./config.mjs";
import { pollUntil } from "./http.mjs";

const otpContextual = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const otpGeneric = /\b(\d{6})\b/;

async function getJSON(path) {
  const res = await fetch(config.mailpitURL + path, {
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  });
  if (!res.ok) throw new Error(`mailpit ${path}: HTTP ${res.status}`);
  return res.json();
}

export function extractOtp(...parts) {
  const combined = parts.filter(Boolean).join("\n");
  const ctxMatch = otpContextual.exec(combined);
  if (ctxMatch) return ctxMatch[1];
  const genMatch = otpGeneric.exec(combined);
  if (genMatch) return genMatch[1];
  return "";
}

export function extractResetToken(...parts) {
  const combined = parts.filter(Boolean).join("\n");
  const linkMatch = /\/dat-lai-mat-khau\?token=([^&\s"'<>]+)/i.exec(combined);
  if (linkMatch?.[1]) {
    try {
      return decodeURIComponent(linkMatch[1].replace(/[.,;)]+$/, ""));
    } catch {
      return linkMatch[1].replace(/[.,;)]+$/, "");
    }
  }
  const paramMatch = /[?&]token=([A-Za-z0-9._~%-]{16,})/i.exec(combined);
  if (paramMatch?.[1]) {
    try {
      return decodeURIComponent(paramMatch[1]);
    } catch {
      return paramMatch[1];
    }
  }
  return "";
}

/** Snapshots current Mailpit message IDs for `email` so subsequent waits only match new emails. */
export async function snapshotMailpitIds(email) {
  const target = email.trim().toLowerCase();
  const seen = new Set();
  const list = await getJSON(
    `/api/v1/search?query=${encodeURIComponent(`to:"${target}"`)}&limit=50`
  );
  for (const m of list.messages ?? []) {
    if (m?.ID) seen.add(m.ID);
  }
  return seen;
}

/**
 * Waits for a NEW 6-digit OTP email to `email` not present in `seenIds`.
 * Never logs the OTP value.
 */
export async function waitForRecentOTP(email, seenIds = new Set(), options = {}) {
  const target = email.trim().toLowerCase();
  return pollUntil(
    options.label ?? `new OTP email to ${target}`,
    async () => {
      const list = await getJSON(
        `/api/v1/search?query=${encodeURIComponent(`to:"${target}"`)}&limit=20`
      );
      for (const m of list.messages ?? []) {
        if (m?.ID && seenIds.has(m.ID)) continue;
        const full = await getJSON(`/api/v1/message/${encodeURIComponent(m.ID)}`);
        const otp = extractOtp(full.Subject, full.Text, full.HTML, m.Snippet);
        if (otp) {
          if (m?.ID) seenIds.add(m.ID);
          return [true, "", otp];
        }
      }
      return [false, `${list.messages?.length ?? 0} messages, no new OTP yet`];
    },
    { timeoutMs: options.timeoutMs ?? config.pollTimeoutMs }
  );
}

/** Waits for the newest OTP mailed to `email` and returns the 6-digit code. */
export async function waitForOTP(email, options = {}) {
  return waitForRecentOTP(email, options.seenIds ?? new Set(), {
    ...options,
    label: options.label ?? `OTP email to ${email}`,
  });
}

/**
 * Waits for a NEW password reset email to `email` and extracts the reset token.
 * Never logs the reset token value.
 */
export async function waitForPasswordResetToken(email, seenIds = new Set(), options = {}) {
  const target = email.trim().toLowerCase();
  return pollUntil(
    options.label ?? `password reset email to ${target}`,
    async () => {
      const list = await getJSON(
        `/api/v1/search?query=${encodeURIComponent(`to:"${target}"`)}&limit=20`
      );
      for (const m of list.messages ?? []) {
        if (m?.ID && seenIds.has(m.ID)) continue;
        const full = await getJSON(`/api/v1/message/${encodeURIComponent(m.ID)}`);
        const token = extractResetToken(full.Text, full.HTML, m.Snippet);
        if (token) {
          if (m?.ID) seenIds.add(m.ID);
          return [true, "", token];
        }
      }
      return [false, `${list.messages?.length ?? 0} messages, no password reset link yet`];
    },
    { timeoutMs: options.timeoutMs ?? config.pollTimeoutMs }
  );
}
