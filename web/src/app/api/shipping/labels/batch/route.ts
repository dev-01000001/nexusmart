import { roleHasCapability } from "@/lib/admin-rbac";
import { GatewayError } from "@/lib/server/errors";
import { gwRaw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertSession } from "@/lib/server/session";
import { validateTrackingCodeList } from "@/features/seller/sellerValidation";

export const dynamic = "force-dynamic";

interface BatchLabelsBody {
  tracking_codes?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    const session = await assertSession();
    const isSellerWithShop = session.user.role === "seller" && Boolean(session.user.shopId);
    const hasStaffCap =
      roleHasCapability(session.user.role, "orders.read") ||
      roleHasCapability(session.user.role, "carriers.read");
    if (!isSellerWithShop && !hasStaffCap) {
      throw new GatewayError("forbidden");
    }

    const raw = await readJson<BatchLabelsBody>(req);
    const validated = validateTrackingCodeList(raw?.tracking_codes, 50);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const upstream = await gwRaw("/api/v1/shipping/labels/batch", {
      method: "POST",
      body: { tracking_codes: validated.codes },
      accept: "application/pdf, application/json",
    });

    const contentType = upstream.headers.get("content-type") || "application/pdf";
    const headers = new Headers({
      "Content-Type": contentType,
      "Cache-Control": "no-store",
    });
    const disposition = upstream.headers.get("content-disposition");
    if (disposition) {
      headers.set("Content-Disposition", disposition);
    } else {
      headers.set("Content-Disposition", 'attachment; filename="shipping-labels-batch.pdf"');
    }
    applyProxySecurityHeaders(headers, upstream.headers, contentType);

    return new Response(upstream.body, {
      status: upstream.status,
      headers,
    });
  },
  {
    messages: {
      INVALID_TRACKING_CODES: "Vui lòng chọn ít nhất một mã vận đơn hợp lệ để in nhãn gộp.",
      TOO_MANY_TRACKING_CODES: "Chỉ hỗ trợ in tối đa 50 vận đơn mỗi lần.",
    },
  }
);
