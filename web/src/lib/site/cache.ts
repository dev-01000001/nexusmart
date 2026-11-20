// Short-TTL bounded positive & negative in-memory host cache for multi-tenant middleware.
// Deduplicates concurrent lookups per host, evicts expired/oldest entries above maxEntries,
// and never caches transient upstream 5xx/network errors.

export interface CachedSiteHostHit {
  status: "found";
  siteId: string;
  shopId: string;
  primaryHost: string;
}

export interface CachedSiteHostMiss {
  status: "not_found";
}

export type SiteHostLookupResult = CachedSiteHostHit | CachedSiteHostMiss | { status: "error" };

type CacheEntry = (CachedSiteHostHit | CachedSiteHostMiss) & { expiresAt: number };

export interface SiteHostCacheOptions {
  positiveTtlMs?: number;
  negativeTtlMs?: number;
  maxEntries?: number;
  now?: () => number;
}

export interface SiteHostCache {
  get: (host: string) => CachedSiteHostHit | CachedSiteHostMiss | null;
  setFound: (host: string, value: Omit<CachedSiteHostHit, "status">) => void;
  setNotFound: (host: string) => void;
  resolve: (
    host: string,
    fetcher: (host: string) => Promise<SiteHostLookupResult>
  ) => Promise<SiteHostLookupResult>;
  delete: (host: string) => void;
  clear: () => void;
  size: () => number;
}

export function createSiteHostCache(opts: SiteHostCacheOptions = {}): SiteHostCache {
  const positiveTtlMs = Math.max(1, opts.positiveTtlMs ?? 3000);
  const negativeTtlMs = Math.max(1, opts.negativeTtlMs ?? 1500);
  const maxEntries = Math.max(1, opts.maxEntries ?? 2000);
  const now = opts.now ?? (() => Date.now());

  const store = new Map<string, CacheEntry>();
  const inflight = new Map<string, Promise<SiteHostLookupResult>>();

  function evictIfNeeded(currentTime: number): void {
    if (store.size < maxEntries) return;
    for (const [k, entry] of store) {
      if (entry.expiresAt <= currentTime) {
        store.delete(k);
      }
    }
    while (store.size >= maxEntries) {
      const oldestKey = store.keys().next().value;
      if (oldestKey === undefined) break;
      store.delete(oldestKey);
    }
  }

  function get(host: string): CachedSiteHostHit | CachedSiteHostMiss | null {
    const key = host.toLowerCase();
    const entry = store.get(key);
    if (!entry) return null;
    if (entry.expiresAt <= now()) {
      store.delete(key);
      return null;
    }
    if (entry.status === "found") {
      return {
        status: "found",
        siteId: entry.siteId,
        shopId: entry.shopId,
        primaryHost: entry.primaryHost,
      };
    }
    return { status: "not_found" };
  }

  function setFound(host: string, value: Omit<CachedSiteHostHit, "status">): void {
    const key = host.toLowerCase();
    const t = now();
    store.delete(key);
    evictIfNeeded(t);
    store.set(key, {
      status: "found",
      ...value,
      expiresAt: t + positiveTtlMs,
    });
  }

  function setNotFound(host: string): void {
    const key = host.toLowerCase();
    const t = now();
    store.delete(key);
    evictIfNeeded(t);
    store.set(key, {
      status: "not_found",
      expiresAt: t + negativeTtlMs,
    });
  }

  async function resolve(
    host: string,
    fetcher: (host: string) => Promise<SiteHostLookupResult>
  ): Promise<SiteHostLookupResult> {
    const key = host.toLowerCase();
    const hit = get(key);
    if (hit) return hit;

    const pending = inflight.get(key);
    if (pending) return pending;

    const task = fetcher(key)
      .then((res) => {
        if (res.status === "found") {
          setFound(key, {
            siteId: res.siteId,
            shopId: res.shopId,
            primaryHost: res.primaryHost,
          });
        } else if (res.status === "not_found") {
          setNotFound(key);
        }
        return res;
      })
      .catch((): SiteHostLookupResult => ({ status: "error" }))
      .finally(() => {
        inflight.delete(key);
      });

    inflight.set(key, task);
    return task;
  }

  return {
    get,
    setFound,
    setNotFound,
    resolve,
    delete: (host: string) => {
      store.delete(host.toLowerCase());
    },
    clear: () => {
      store.clear();
      inflight.clear();
    },
    size: () => store.size,
  };
}
