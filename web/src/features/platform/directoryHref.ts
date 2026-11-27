// Pure pagination URL builders for the root-host site directory (`/` and `/cua-hang`).
// Kept dependency-free so Server Components and `node --test` can both import it directly.

export function homeDirectoryPageHref(page: number): string {
  return page <= 1 ? "/#danh-ba-gian-hang" : `/?trang=${page}#danh-ba-gian-hang`;
}

export function storesDirectoryPageHref(page: number): string {
  return page <= 1 ? "/cua-hang" : `/cua-hang?trang=${page}`;
}
