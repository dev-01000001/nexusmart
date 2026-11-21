const OTP_CONTEXTUAL_RE = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const OTP_GENERIC_RE = /\b(\d{6})\b/;

interface MailpitSearchResponse {
  messages?: Array<{
    ID: string;
    Subject?: string;
    Created?: string;
  }>;
}

interface MailpitMessageDetail {
  ID: string;
  Subject?: string;
  Text?: string;
  HTML?: string;
}

export interface WaitForOtpOptions {
  mailpitUrl?: string;
  timeoutMs?: number;
  pollIntervalMs?: number;
  httpTimeoutMs?: number;
}

export function getMailpitUrl(): string {
  return (process.env.MAILPIT_URL?.trim() || "http://127.0.0.1:8025").replace(/\/+$/, "");
}

async function fetchMailpitJson<T>(baseUrl: string, path: string, httpTimeoutMs: number): Promise<T> {
  const response = await fetch(`${baseUrl}${path}`, {
    signal: AbortSignal.timeout(httpTimeoutMs),
  });
  if (!response.ok) {
    throw new Error(`Mailpit GET ${path} failed: HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Polls Mailpit for the newest email addressed to `email` and extracts its 6-digit OTP code.
 */
export async function waitForOtp(email: string, options: WaitForOtpOptions = {}): Promise<string> {
  const mailpitUrl = (options.mailpitUrl?.trim() || getMailpitUrl()).replace(/\/+$/, "");
  const timeoutMs = options.timeoutMs ?? 30_000;
  const pollIntervalMs = options.pollIntervalMs ?? 500;
  const httpTimeoutMs = options.httpTimeoutMs ?? 10_000;

  const deadline = Date.now() + timeoutMs;
  let lastNote = "no poll attempts completed";

  while (Date.now() < deadline) {
    try {
      const searchPath = `/api/v1/search?query=${encodeURIComponent(`to:"${email}"`)}&limit=10`;
      const list = await fetchMailpitJson<MailpitSearchResponse>(
        mailpitUrl,
        searchPath,
        httpTimeoutMs
      );
      const messages = list.messages ?? [];
      for (const msg of messages) {
        const detail = await fetchMailpitJson<MailpitMessageDetail>(
          mailpitUrl,
          `/api/v1/message/${encodeURIComponent(msg.ID)}`,
          httpTimeoutMs
        );
        const body = `${detail.Subject ?? ""}\n${detail.Text || detail.HTML || ""}`;
        const match = body.match(OTP_CONTEXTUAL_RE) ?? body.match(OTP_GENERIC_RE);
        if (match) {
          return match[1];
        }
      }
      lastNote = `${messages.length} message(s) found, no 6-digit OTP matched yet`;
    } catch (err) {
      lastNote = err instanceof Error ? err.message : String(err);
    }

    const remaining = deadline - Date.now();
    if (remaining <= 0) break;
    await sleep(Math.min(pollIntervalMs, remaining));
  }

  throw new Error(
    `Timed out after ${timeoutMs}ms waiting for 6-digit OTP email to "${email}" via ${mailpitUrl} (${lastNote})`
  );
}
