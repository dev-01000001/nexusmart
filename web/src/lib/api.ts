// Browser-side helper for calling this app's own /api/* route handlers (the BFF).
// Same-origin fetch: cookies ride along automatically and the browser sets `Origin` on writes.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message); // already a safe Vietnamese message from the BFF
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export interface ApiOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

/** Throws ApiError (status 0 = network failure). Returns undefined for 204. */
export async function api<T>(path: string, opts: ApiOptions = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method: opts.method ?? "GET",
      headers: {
        Accept: "application/json",
        ...(opts.body !== undefined ? { "Content-Type": "application/json" } : {}),
        ...opts.headers,
      },
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      credentials: "same-origin",
      cache: "no-store",
      signal: opts.signal,
    });
  } catch {
    throw new ApiError(0, "NETWORK", "Không kết nối được. Bạn kiểm tra mạng và thử lại nhé.");
  }
  if (res.status === 204) return undefined as T;
  const data = (await res.json().catch(() => null)) as
    | (T & { error?: { code?: string; message?: string } })
    | null;
  if (!res.ok) {
    throw new ApiError(
      res.status,
      data?.error?.code ?? "UNAVAILABLE",
      data?.error?.message ?? "Hệ thống đang bận. Bạn vui lòng thử lại sau."
    );
  }
  return data as T;
}
