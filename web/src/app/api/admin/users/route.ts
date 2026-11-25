import { ALL_USER_ROLES } from "@/lib/admin-rbac";
import type { GatewayUser } from "@/lib/gateway-types";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  ALLOWED_USER_QUERY_STATUSES,
  parseOptionalPositiveInt,
} from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(async (req) => {
  await assertCapability("users.read");
  const params = req.nextUrl.searchParams;
  const searchTerm =
    (params.get("query") ?? params.get("q") ?? "").trim().slice(0, 120) || undefined;
  const rawRole = (params.get("role") ?? "").trim().toLowerCase();
  const role = (ALL_USER_ROLES as readonly string[]).includes(rawRole) ? rawRole : undefined;
  const rawStatus = (params.get("status") ?? "").trim().toUpperCase();
  const status = (ALLOWED_USER_QUERY_STATUSES as readonly string[]).includes(rawStatus)
    ? rawStatus
    : undefined;

  const data = await gw<{ items: GatewayUser[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/users",
    {
      query: {
        query: searchTerm,
        role,
        status,
        page: parseOptionalPositiveInt(params.get("page"), 10000),
        page_size: parseOptionalPositiveInt(params.get("page_size"), 100),
      },
    }
  );
  return Response.json(
    {
      items: data.items ?? [],
      page: data.page ?? 1,
      page_size: data.page_size ?? 20,
      total: data.total ?? 0,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
