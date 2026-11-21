// Server-only helpers for Seller Site Editor, Draft Preview, and BFF Site routes.
import { revalidatePath, revalidateTag } from "next/cache";
import { headers } from "next/headers";
import { getSiteRootDomain as readConfiguredRootDomain } from "@/lib/server/config";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { parseSiteConfig } from "@/lib/site/types";
import type { GwSellerShop } from "@/features/seller/sellerApiTypes";
import type { GwSite, GwSiteVersion } from "./siteEditorTypes";
import { summarizeSellerSite, type SellerSiteSummary } from "./siteSummary";

export function getSiteRootDomain(): string {
  return readConfiguredRootDomain();
}

export interface RawGwSite {
  id?: string;
  shop_id?: string;
  shop_name?: string;
  subdomain?: string;
  primary_host?: string;
  host?: string;
  status?: string;
  draft_config?: unknown;
  draft_config_json?: string;
  published_config?: unknown;
  published_config_json?: string;
  config?: unknown;
  config_json?: string;
  published_version_id?: string | null;
  published_version_number?: number;
  published_at?: string | null;
  suspended_reason?: string;
  created_at?: string;
  updated_at?: string;
}

export interface RawGwSiteVersion {
  id?: string;
  site_id?: string;
  version_number?: number;
  config?: unknown;
  config_json?: string;
  published_by?: string;
  note?: string;
  created_at?: string;
}

export function normalizeGwSite(raw: RawGwSite | null | undefined): GwSite | null {
  if (!raw || !raw.id) return null;
  const rootDomain = getSiteRootDomain();
  const subdomain = (raw.subdomain ?? "").trim().toLowerCase();
  const primaryHost = (
    raw.primary_host ||
    raw.host ||
    (subdomain ? `${subdomain}.${rootDomain}` : "")
  )
    .trim()
    .toLowerCase();

  const draftConfig = parseSiteConfig(
    raw.draft_config ?? raw.draft_config_json ?? raw.config ?? raw.config_json,
    raw.shop_name ?? ""
  );

  return {
    id: raw.id,
    shop_id: raw.shop_id ?? "",
    shop_name: raw.shop_name,
    subdomain,
    primary_host: primaryHost,
    status: raw.status || "DRAFT",
    config: draftConfig,
    draft_config: draftConfig,
    published_config: parseSiteConfig(
      raw.published_config ?? raw.published_config_json,
      raw.shop_name ?? ""
    ),
    published_version_id: raw.published_version_id ?? "",
    published_version_number: raw.published_version_number,
    published_at: raw.published_at ?? undefined,
    suspended_reason: raw.suspended_reason ?? "",
    created_at: raw.created_at,
    updated_at: raw.updated_at,
  };
}

export function normalizeGwSiteVersion(raw: RawGwSiteVersion): GwSiteVersion {
  return {
    id: raw.id ?? "",
    site_id: raw.site_id ?? "",
    version_number: Number(raw.version_number) || 0,
    config: parseSiteConfig(raw.config ?? raw.config_json),
    published_by: raw.published_by ?? "",
    note: raw.note ?? "",
    created_at: raw.created_at,
  };
}

/**
 * Revalidate all Next.js cache tags & routes for a site after publish, rollback, or suspend/unsuspend.
 */
export function revalidateSiteCache(
  site: Pick<GwSite, "id" | "shop_id" | "primary_host"> | null | undefined
): void {
  if (!site) return;
  try {
    if (site.id) {
      revalidateTag(`site:${site.id}`);
    }
    if (site.shop_id) {
      revalidateTag(`site-shop:${site.shop_id}`);
    }
    if (site.primary_host) {
      const host = site.primary_host.trim().toLowerCase();
      revalidateTag(`site-host:${host}`);
      revalidatePath(`/s/${host}`, "layout");
    }
  } catch {
    // Revalidation is best-effort outside static-cache contexts.
  }
}

/**
 * Inspect gateway error detail on the server (never leaking raw backend strings) to choose a
 * helpful Vietnamese validation message for the seller or admin UI.
 */
export function resolveSiteErrorMessage(err: unknown): {
  status: number;
  code: string;
  message: string;
} {
  if (!(err instanceof GatewayError)) {
    return { status: 503, code: "UNAVAILABLE", message: SAFE_MESSAGES.unavailable };
  }
  const detail = err.detail.toLowerCase();
  const code = err.code.toUpperCase();

  if (
    code === "ALREADY_EXISTS" ||
    detail.includes("subdomain already") ||
    detail.includes("already exists")
  ) {
    return {
      status: 409,
      code: "SUBDOMAIN_TAKEN",
      message: "Tên miền phụ này đã có gian hàng khác sử dụng. Vui lòng chọn tên miền khác.",
    };
  }
  if (detail.includes("contrast") || detail.includes("wcag")) {
    return {
      status: 400,
      code: "LOW_CONTRAST",
      message:
        "Độ tương phản giữa màu chữ và màu nền chưa đạt tối thiểu 4,5:1. Vui lòng điều chỉnh màu rõ nét hơn.",
    };
  }
  if (
    detail.includes("reserved") ||
    detail.includes("forbidden") ||
    detail.includes("impersonat")
  ) {
    return {
      status: 400,
      code: "SUBDOMAIN_RESERVED",
      message:
        "Tên miền phụ thuộc danh sách bảo vệ hệ thống hoặc nhãn hiệu. Vui lòng chọn tên khác.",
    };
  }
  if (detail.includes("subdomain")) {
    return {
      status: 400,
      code: "INVALID_SUBDOMAIN",
      message:
        "Tên miền phụ cần từ 3 đến 40 ký tự gồm chữ thường (a-z), số (0-9) và dấu gạch nối (-).",
    };
  }
  if (
    detail.includes("url") ||
    detail.includes("href") ||
    detail.includes("javascript:") ||
    detail.includes("data:") ||
    detail.includes("scheme")
  ) {
    return {
      status: 400,
      code: "INVALID_URL",
      message:
        "Đường dẫn ảnh hoặc liên kết không hợp lệ. Chỉ chấp nhận đường dẫn nội bộ (/) hoặc https://.",
    };
  }
  if (detail.includes("section") && (detail.includes("20") || detail.includes("max"))) {
    return {
      status: 400,
      code: "TOO_MANY_SECTIONS",
      message: "Site riêng hỗ trợ tối đa 20 khối nội dung. Vui lòng bớt khối trước khi lưu.",
    };
  }
  if (detail.includes("unknown field") || detail.includes("trailing")) {
    return {
      status: 400,
      code: "INVALID_SCHEMA",
      message: "Cấu hình giao diện chứa trường dữ liệu không được hỗ trợ. Vui lòng kiểm tra lại.",
    };
  }
  if (detail.includes("font")) {
    return {
      status: 400,
      code: "INVALID_FONT",
      message: "Phông chữ đã chọn không nằm trong danh sách được hỗ trợ.",
    };
  }
  if (detail.includes("suspended")) {
    return {
      status: 409,
      code: "SITE_SUSPENDED",
      message:
        "Site riêng đang bị tạm khóa bởi quản trị NexusMart, chưa thể xuất bản hoặc khôi phục.",
    };
  }
  if (detail.includes("shop") && detail.includes("active")) {
    return {
      status: 409,
      code: "SHOP_NOT_ACTIVE",
      message: "Gian hàng cần ở trạng thái đang hoạt động trước khi xuất bản site riêng.",
    };
  }
  if (err.kind === "invalid") {
    return {
      status: 400,
      code: err.code || "INVALID_ARGUMENT",
      message:
        "Thông tin cấu hình site chưa hợp lệ. Vui lòng kiểm tra lại tên miền, màu sắc và các khối nội dung.",
    };
  }
  if (err.kind === "not_found") {
    return {
      status: 404,
      code: err.code || "NOT_FOUND",
      message: "Không tìm thấy cấu hình site hoặc phiên bản yêu cầu.",
    };
  }
  if (err.kind === "conflict") {
    return {
      status: 409,
      code: err.code || "CONFLICT",
      message:
        "Thao tác chưa thể thực hiện ở trạng thái hiện tại. Vui lòng tải lại trang và thử lại.",
    };
  }
  return {
    status: err.status,
    code: err.code,
    message: SAFE_MESSAGES[err.kind],
  };
}

export async function loadSellerSiteServer(): Promise<{
  site: GwSite | null;
  shop: GwSellerShop | null;
  rootDomain: string;
  error: string | null;
}> {
  const rootDomain = getSiteRootDomain();
  const [siteResult, shopResult] = await Promise.all([
    gw<{ site?: RawGwSite | null; shop?: GwSellerShop | null; root_domain?: string }>(
      "/api/v1/seller/site"
    )
      .then((res) => ({ data: res, error: null as string | null }))
      .catch((err) => {
        if (err instanceof GatewayError && err.kind === "not_found") {
          return { data: { site: null, shop: null, root_domain: rootDomain }, error: null };
        }
        return {
          data: null,
          error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
        };
      }),
    gw<{ shop: GwSellerShop | null }>("/api/v1/seller/shop")
      .then((res) => res.shop ?? null)
      .catch(() => null),
  ]);

  if (siteResult.error) {
    return { site: null, shop: shopResult, rootDomain, error: siteResult.error };
  }

  const resolvedRoot = siteResult.data?.root_domain?.trim().toLowerCase() || rootDomain;
  return {
    site: normalizeGwSite(siteResult.data?.site),
    shop: siteResult.data?.shop ?? shopResult,
    rootDomain: resolvedRoot,
    error: null,
  };
}

export async function loadSellerSiteVersionsServer(
  pageSize = 50
): Promise<{ versions: GwSiteVersion[]; total: number; error: string | null }> {
  try {
    const res = await gw<{
      versions?: RawGwSiteVersion[];
      items?: RawGwSiteVersion[];
      total?: number;
    }>("/api/v1/seller/site/versions", {
      query: { page: 1, page_size: pageSize },
    });
    const list = (res.versions ?? res.items ?? []).map(normalizeGwSiteVersion);
    return { versions: list, total: res.total ?? list.length, error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { versions: [], total: 0, error: null };
    }
    return {
      versions: [],
      total: 0,
      error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
    };
  }
}

/**
 * Site status for Seller Center chrome (shell + overview). Never throws: a gateway failure is
 * reported as "none" so the shell still renders; the site editor shows the real error.
 */
export async function loadSellerSiteSummaryServer(): Promise<SellerSiteSummary> {
  const site = await gw<{ site?: RawGwSite | null }>("/api/v1/seller/site")
    .then((res) => normalizeGwSite(res.site))
    .catch(() => null);
  const h = await headers();
  const rawHost = resolveRequestHost(h, "");
  const proto = resolveRequestProto(h, `http://${rawHost || "localhost"}`);
  return summarizeSellerSite(site, rawHost, proto);
}
