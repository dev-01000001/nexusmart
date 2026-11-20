import { config } from "./config.mjs";
import { pollUntil } from "./http.mjs";

const otpContextual = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const otpGeneric = /\b(\d{6})\b/;

async function getJSON(path) {
  const res = await fetch(config.mailpitURL + path, { signal: AbortSignal.timeout(config.httpTimeoutMs) });
  if (!res.ok) throw new Error(`mailpit ${path}: HTTP ${res.status}`);
  return res.json();
}

/** Waits for the newest OTP mailed to `email` and returns the 6-digit code. */
export async function waitForOTP(email) {
  return pollUntil(`OTP email to ${email}`, async () => {
    const list = await getJSON(`/api/v1/search?query=${encodeURIComponent(`to:"${email}"`)}&limit=10`);
    for (const m of list.messages ?? []) {
      const full = await getJSON(`/api/v1/message/${m.ID}`);
      const text = `${full.Subject}\n${full.Text || full.HTML || ""}`;
      const match = text.match(otpContextual) ?? text.match(otpGeneric);
      if (match) return [true, "", match[1]];
    }
    return [false, `${list.messages?.length ?? 0} messages, no OTP yet`];
  });
}
