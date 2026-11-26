export interface NavMatchItem {
  href: string;
  match?: "exact" | "prefix";
  children?: ReadonlyArray<{ href: string }>;
}

export interface ActiveNavResult {
  activeHref: string | null;
  activeChildHref: string | null;
}

type NavMatchScore = [number, number];

function parseHref(href: string): { cleanHref: string; hrefQuery: string } {
  const hrefWithoutHash = href.split("#")[0] ?? "";
  const qIdx = hrefWithoutHash.indexOf("?");
  const rawHrefPath = qIdx === -1 ? hrefWithoutHash : hrefWithoutHash.slice(0, qIdx);
  const hrefQuery = qIdx === -1 ? "" : hrefWithoutHash.slice(qIdx + 1);
  const cleanHref = rawHrefPath.replace(/\/+$/, "") || "/";
  return { cleanHref, hrefQuery };
}

export function isNavItemActive(
  pathname: string,
  search: string,
  href: string,
  homeHref: string,
  match?: "exact" | "prefix"
): boolean {
  const cleanPath = pathname.replace(/\/+$/, "") || "/";
  const { cleanHref, hrefQuery } = parseHref(href);
  const cleanHome = (homeHref.split("?")[0] ?? "").replace(/\/+$/, "") || "/";
  const mode = match ?? (cleanHref === cleanHome ? "exact" : "prefix");

  const pathMatches =
    mode === "exact"
      ? cleanPath === cleanHref
      : cleanPath === cleanHref || cleanPath.startsWith(`${cleanHref}/`);

  if (!pathMatches) return false;
  if (!hrefQuery) return true;

  const targetParams = new URLSearchParams(hrefQuery);
  const currentParams = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  for (const [key, value] of targetParams.entries()) {
    if (!currentParams.getAll(key).includes(value)) {
      return false;
    }
  }
  return true;
}

function scoreHref(
  pathname: string,
  search: string,
  href: string,
  homeHref: string,
  match?: "exact" | "prefix"
): NavMatchScore | null {
  if (!isNavItemActive(pathname, search, href, homeHref, match)) {
    return null;
  }
  const { cleanHref, hrefQuery } = parseHref(href);
  const queryCount = hrefQuery ? Array.from(new URLSearchParams(hrefQuery).entries()).length : 0;
  return [queryCount, cleanHref.length];
}

function isHigherScore(candidate: NavMatchScore, best: NavMatchScore | null): boolean {
  if (best === null) return true;
  if (candidate[0] !== best[0]) return candidate[0] > best[0];
  return candidate[1] > best[1];
}

/**
 * Resolve at most one active top-level item and one active child item.
 *
 * Score of a matching href is `[matchedQueryParamCount, normalizedPathLength]`
 * compared lexicographically; non-matching hrefs score `null`.
 * - Top-level item score = highest score between itself (`item.match`) and its children (`"prefix"`).
 * - Child score = score among the winning top-level item's children (`"exact"`).
 * - Tie-breaker: strictly greater (`>`) replaces the current winner, so on a tie the earlier item in the list wins.
 */
export function resolveActiveNav(
  items: readonly NavMatchItem[],
  pathname: string,
  search: string,
  homeHref: string
): ActiveNavResult {
  let bestItem: NavMatchItem | null = null;
  let bestItemScore: NavMatchScore | null = null;

  for (const item of items) {
    let itemScore = scoreHref(pathname, search, item.href, homeHref, item.match);
    if (item.children) {
      for (const child of item.children) {
        const childScore = scoreHref(pathname, search, child.href, homeHref, "prefix");
        if (childScore !== null && isHigherScore(childScore, itemScore)) {
          itemScore = childScore;
        }
      }
    }
    if (itemScore !== null && isHigherScore(itemScore, bestItemScore)) {
      bestItem = item;
      bestItemScore = itemScore;
    }
  }

  if (!bestItem) {
    return { activeHref: null, activeChildHref: null };
  }

  let bestChildHref: string | null = null;
  let bestChildScore: NavMatchScore | null = null;
  if (bestItem.children) {
    for (const child of bestItem.children) {
      const childScore = scoreHref(pathname, search, child.href, homeHref, "exact");
      if (childScore !== null && isHigherScore(childScore, bestChildScore)) {
        bestChildHref = child.href;
        bestChildScore = childScore;
      }
    }
  }

  return {
    activeHref: bestItem.href,
    activeChildHref: bestChildHref,
  };
}
