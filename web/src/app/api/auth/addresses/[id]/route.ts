import {
  type AddressPayloadInput,
  type GatewayAddress,
  toAddressItem,
  toGatewayAddressBody,
} from "@/lib/server/address";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";

interface RouteContext {
  params: Promise<{ id: string }>;
}

const updateHandler = apiRoute<RouteContext>(
  async (req, ctx) => {
    await assertSession();
    const { id } = await ctx.params;
    const cleanId = id?.trim();
    if (!cleanId) throw new GatewayError("invalid", "INVALID_ARGUMENT");

    const input = await readJson<AddressPayloadInput>(req);
    const body = toGatewayAddressBody(input);
    const res = await gw<{ address: GatewayAddress }>(
      `/api/v1/users/me/addresses/${encodeURIComponent(cleanId)}`,
      {
        method: "PUT",
        body,
      }
    );

    return Response.json(
      { address: toAddressItem(res.address) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      NOT_FOUND: "Không tìm thấy địa chỉ này.",
      INVALID_ARGUMENT: "Thông tin địa chỉ chưa hợp lệ. Bạn vui lòng kiểm tra lại.",
    },
  }
);

export const PUT = updateHandler;
export const PATCH = updateHandler;

export const DELETE = apiRoute<RouteContext>(
  async (_req, ctx) => {
    await assertSession();
    const { id } = await ctx.params;
    const cleanId = id?.trim();
    if (!cleanId) throw new GatewayError("invalid", "INVALID_ARGUMENT");

    const res = await gw<{ ok?: boolean }>(
      `/api/v1/users/me/addresses/${encodeURIComponent(cleanId)}`,
      { method: "DELETE" }
    );

    return Response.json({ ok: Boolean(res.ok) }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      NOT_FOUND: "Không tìm thấy địa chỉ cần xóa.",
    },
  }
);
