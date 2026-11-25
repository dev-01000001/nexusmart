import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { PLATFORM_TEMPLATES } from "@/features/site/templateCatalog";
import type { PlatformTemplateDTO } from "@/features/site/templateTypes";
import { normalizeSiteTemplateConfig } from "@/features/site/templateValidator";

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

function normalizeGatewayTemplates(rawList: RawTemplateDTO[]): PlatformTemplateDTO[] {
  const out: PlatformTemplateDTO[] = [];
  for (const raw of rawList) {
    const id = (raw.id || raw.slug || "").trim().toLowerCase();
    if (!id) continue;
    const cfg = normalizeSiteTemplateConfig(raw.config ?? raw.config_json, raw.name || "");
    if (!cfg) continue;
    out.push({
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
    });
  }
  return out;
}

export const GET = apiRoute(async () => {
  try {
    const data = await gw<{
      templates?: RawTemplateDTO[];
      items?: RawTemplateDTO[];
      total?: number;
    }>("/api/v1/site-templates", { auth: "none" });
    const rawList = data.templates ?? data.items ?? [];
    const normalized = normalizeGatewayTemplates(rawList);
    if (normalized.length > 0) {
      return Response.json(
        {
          templates: normalized,
          total: data.total ?? normalized.length,
          source: "gateway",
        },
        { headers: { "Cache-Control": "public, max-age=60" } }
      );
    }
    return Response.json(
      {
        templates: PLATFORM_TEMPLATES,
        total: PLATFORM_TEMPLATES.length,
        source: "fallback",
      },
      { headers: { "Cache-Control": "public, max-age=60" } }
    );
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "unavailable" || err.kind === "not_found")) {
      return Response.json(
        {
          templates: PLATFORM_TEMPLATES,
          total: PLATFORM_TEMPLATES.length,
          source: "fallback",
        },
        { headers: { "Cache-Control": "public, max-age=60" } }
      );
    }
    throw err;
  }
});
