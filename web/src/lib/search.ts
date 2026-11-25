import { PRODUCTS, type Product } from "../data/products";
import { SHOPS } from "../data/shops";

/**
 * Strips Vietnamese diacritics, maps đ/Đ -> d, lowercases and collapses whitespace.
 */
export function normalizeVietnamese(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * @deprecated Legacy client-side synonym map superseded by server SearchService (B1).
 * Retained solely for unowned legacy callers (`ShopDetailClient.tsx`, `scripts/check-data.mjs`).
 * Storefront header & catalog/search pages use `@/lib/search-client`.
 */
export const SEARCH_SYNONYMS: Record<string, string[]> = {
  "ao phong": ["ao thun", "tshirt", "t-shirt", "tee", "cotton"],
  "ao thun": ["ao phong", "tshirt", "tee"],
  tshirt: ["ao thun", "ao phong"],
  tee: ["ao thun", "ao phong"],
  "quan dui": ["quan short", "short"],
  "quan coc": ["quan short", "short"],
  "quan soc": ["quan short", "short"],
  "quan short": ["quan dui", "quan coc", "short"],
  "quan sip": ["do lot", "boxer", "brief"],
  sip: ["do lot", "boxer", "brief"],
  "quan lot": ["do lot", "boxer", "brief"],
  "do lot": ["quan sip", "quan lot", "boxer", "brief"],
  vo: ["tat", "socks"],
  tat: ["vo", "tat co trung", "tat co thap"],
  "ao gio": ["ao khoac gio", "ao khoac", "ripstop"],
  "ao khoac": ["ao gio", "ripstop"],
  jacket: ["ao khoac", "ao gio"],
  "co be": ["ao polo", "polo"],
  polo: ["ao polo", "pique", "det kim"],
  "the thao": ["dryfit", "dry-fit", "chay bo", "gym"],
  "chay bo": ["dryfit", "short run", "5 inch", "the thao"],
  "quan dai": ["quan jogger", "jogger"],
  "quan ni": ["quan jogger", "jogger", "chan cua", "terry"],
  lanh: ["linen", "short linen"],
  tre: ["bamboo", "soi tre"],
  "go soi": ["modal", "soi modal"],
};

const SPELLING_DICTIONARY: Array<{ normalized: string; display: string }> = [
  { normalized: "ao thun", display: "áo thun" },
  { normalized: "ao phong", display: "áo phông" },
  { normalized: "ao polo", display: "áo polo" },
  { normalized: "ao khoac gio", display: "áo khoác gió" },
  { normalized: "quan short", display: "quần short" },
  { normalized: "quan jogger", display: "quần jogger" },
  { normalized: "do lot", display: "đồ lót" },
  { normalized: "boxer modal", display: "boxer modal" },
  { normalized: "tat cotton", display: "tất cotton" },
  { normalized: "chay bo", display: "chạy bộ" },
  { normalized: "dryfit", display: "dry-fit" },
  { normalized: "cotton 220gsm", display: "cotton 220gsm" },
  { normalized: "linen", display: "linen" },
  { normalized: "oversize", display: "oversize" },
  { normalized: "pique", display: "pique" },
  { normalized: "outlet", display: "outlet" },
];

function expandQueryTerms(normalizedQuery: string): string[] {
  const expanded = new Set<string>([normalizedQuery]);
  for (const [phrase, synonyms] of Object.entries(SEARCH_SYNONYMS)) {
    if (normalizedQuery === phrase || normalizedQuery.includes(phrase)) {
      for (const syn of synonyms) {
        expanded.add(normalizedQuery.replace(phrase, syn).trim());
        expanded.add(syn);
      }
    }
  }
  return Array.from(expanded).filter(Boolean);
}

function buildProductSearchText(product: Product): string {
  const realShopName =
    "shopName" in product && typeof (product as { shopName?: unknown }).shopName === "string"
      ? ((product as { shopName: string }).shopName ?? "")
      : "";
  const realShopShortName =
    "shopShortName" in product &&
    typeof (product as { shopShortName?: unknown }).shopShortName === "string"
      ? ((product as { shopShortName: string }).shopShortName ?? "")
      : "";
  const shop = realShopName ? undefined : SHOPS.find((s) => s.id === product.shopId);
  const parts = [
    product.name,
    product.shortName,
    product.slug.replace(/-/g, " "),
    product.categoryName,
    product.fabric,
    product.materialLabel ?? "",
    product.fit,
    product.origin,
    product.gsm > 0 ? `${product.gsm}gsm` : "",
    product.description,
    realShopName || shop?.name || "",
    realShopShortName,
    shop?.city ?? "",
    ...product.colors.map((c) => c.name),
    ...(product.occasion ?? []).map((o) => o.replace(/-/g, " ")),
    ...(product.sport ?? []).map((s) => s.replace(/-/g, " ")),
    ...(product.fabricFeatures ?? []).map((f) => f.replace(/-/g, " ")),
    ...(product.badges ?? []),
  ];
  return normalizeVietnamese(parts.join(" "));
}

export function searchProducts(
  query: string,
  catalog: Product[] = PRODUCTS,
  options?: { shopId?: string }
): Product[] {
  const clean = normalizeVietnamese(query);
  const pool = options?.shopId ? catalog.filter((p) => p.shopId === options.shopId) : catalog;
  if (!clean) return pool;

  const phrases = expandQueryTerms(clean);
  const baseTokens = clean.split(" ").filter((t) => t.length >= 2);

  const scored: Array<{ product: Product; score: number }> = [];

  for (const product of pool) {
    const haystack = buildProductSearchText(product);
    const titleNorm = normalizeVietnamese(product.name);
    let score = 0;

    if (titleNorm.includes(clean)) {
      score += 100;
    } else if (haystack.includes(clean)) {
      score += 65;
    }

    for (const phrase of phrases) {
      if (phrase === clean) continue;
      if (titleNorm.includes(phrase)) {
        score += 55;
      } else if (haystack.includes(phrase)) {
        score += 35;
      }
    }

    if (baseTokens.length > 0) {
      let matchedTokens = 0;
      for (const tok of baseTokens) {
        const tokenSyns = SEARCH_SYNONYMS[tok] ?? [];
        if (haystack.includes(tok) || tokenSyns.some((s) => haystack.includes(s))) {
          matchedTokens += 1;
        }
      }
      if (matchedTokens === baseTokens.length) {
        score += 40 + matchedTokens * 8;
      } else if (baseTokens.length >= 2 && matchedTokens >= baseTokens.length - 1 && score > 0) {
        score += matchedTokens * 5;
      }
    }

    if (score > 0) {
      scored.push({ product, score });
    }
  }

  return scored
    .sort((a, b) => b.score - a.score || (a.product.salesRank ?? 99) - (b.product.salesRank ?? 99))
    .map((entry) => entry.product);
}

function levenshtein(a: string, b: string): number {
  const m = a.length;
  const n = b.length;
  if (m === 0) return n;
  if (n === 0) return m;
  const prev = new Array<number>(n + 1);
  const curr = new Array<number>(n + 1);
  for (let j = 0; j <= n; j++) prev[j] = j;
  for (let i = 1; i <= m; i++) {
    curr[0] = i;
    for (let j = 1; j <= n; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      curr[j] = Math.min(prev[j] + 1, curr[j - 1] + 1, prev[j - 1] + cost);
    }
    for (let j = 0; j <= n; j++) prev[j] = curr[j];
  }
  return prev[n];
}

/**
 * Returns a Vietnamese spelling suggestion when a query has a near typo.
 */
export function suggestSpelling(query: string): string | null {
  const clean = normalizeVietnamese(query);
  if (clean.length < 3) return null;

  let best: { display: string; dist: number } | null = null;
  for (const entry of SPELLING_DICTIONARY) {
    if (entry.normalized === clean) return null;
    const dist = levenshtein(clean, entry.normalized);
    const threshold = Math.max(2, Math.floor(entry.normalized.length * 0.35));
    if (dist <= threshold && (!best || dist < best.dist)) {
      best = { display: entry.display, dist };
    }
  }
  return best ? best.display : null;
}
