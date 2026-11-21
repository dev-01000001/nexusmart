// Responses for old marketplace URLs on the root host.

export function legacyRedirect(target: string): Response {
  return new Response(null, {
    status: 308,
    headers: { Location: target, "Cache-Control": "no-store" },
  });
}

export function legacyNotFound(): Response {
  return new Response("Not Found", {
    status: 404,
    headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" },
  });
}

/** Route params may arrive encoded or already decoded; never throw on a stray `%`. */
export function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}
