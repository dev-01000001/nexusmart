import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { findPlatformTemplate } from "@/features/site/templateCatalog";
import type { PlatformTemplateDTO } from "@/features/site/templateTypes";
import { normalizeSiteTemplateConfig } from "@/features/site/templateValidator";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface RawTemplateDTO {
  id?: string;
  slug?: string;
  name?: string;
  category?: string;
  archetype?: string;
  description?: string;
  preview_image_url?: string;
  tags?: string[];
  config?: unknown;
  config_json?: string;
}

const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

function normalizeOne(raw: RawTemplateDTO | null | undefined): PlatformTemplateDTO | null {
  if (!raw) return null;
  const id = (raw.id || raw.slug || "").trim().toLowerCase();
  if (!id) return null;
  const cfg = normalizeSiteTemplateConfig(raw.config ?? raw.config_json, raw.name || "");
  if (!cfg) return null;
  return {
    id,
    slug: (raw.slug || id).trim().toLowerCase(),
    name: (raw.name || id).trim(),
    category: (raw.category || "Tổng hợp").trim(),
    archetype: cfg.layout.archetype,
    description: (raw.description || "").trim(),
    preview_image_url: (raw.preview_image_url || cfg.theme.banner_url || "").trim(),
    tags: Array.isArray(raw.tags)
      ? raw.tags.filter((t): t is string => typeof t === "string" && Boolean(t.trim()))
      : [],
    config: cfg,
  };
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const { id } = await ctx.params;
  const cleanId = decodeURIComponent(id || "")
    .trim()
    .toLowerCase();
  if (!cleanId || !SLUG_RE.test(cleanId)) {
    return Response.json(
      { error: { code: "TEMPLATE_NOT_FOUND", message: "Không tìm thấy mẫu giao diện yêu cầu." } },
      { status: 404, headers: { "Cache-Control": "no-store" } }
    );
  }

  try {
    const data = await gw<{ template?: RawTemplateDTO | null }>(
      `/api/v1/site-templates/${encodeURIComponent(cleanId)}`,
      { auth: "none" }
    );
    const tpl = normalizeOne(data.template);
    if (!tpl) {
      return Response.json(
        { error: { code: "TEMPLATE_NOT_FOUND", message: "Không tìm thấy mẫu giao diện yêu cầu." } },
        { status: 404, headers: { "Cache-Control": "no-store" } }
      );
    }
    return Response.json(
      { template: tpl, source: "gateway" },
      { headers: { "Cache-Control": "public, max-age=60" } }
    );
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "unavailable" || err.kind === "not_found")) {
      const fallback = findPlatformTemplate(cleanId);
      if (fallback) {
        return Response.json(
          { template: fallback, source: "fallback" },
          { headers: { "Cache-Control": "public, max-age=60" } }
        );
      }
      return Response.json(
        { error: { code: "TEMPLATE_NOT_FOUND", message: "Không tìm thấy mẫu giao diện yêu cầu." } },
        { status: 404, headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
});
