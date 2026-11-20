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

export const GET = apiRoute(async () => {
  await assertSession();
  const res = await gw<{ addresses?: GatewayAddress[] }>("/api/v1/users/me/addresses");
  const addresses = (res.addresses ?? []).map(toAddressItem);
  return Response.json({ addresses }, { headers: { "Cache-Control": "no-store" } });
});

export const POST = apiRoute(
  async (req) => {
    await assertSession();
    const input = await readJson<AddressPayloadInput>(req);
    const body = toGatewayAddressBody(input);
    if (
      !body.recipient_name ||
      !body.recipient_phone ||
      !body.province_code ||
      !body.street_address
    ) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<{ address: GatewayAddress }>("/api/v1/users/me/addresses", {
      method: "POST",
      body,
    });

    return Response.json(
      { address: toAddressItem(res.address) },
      { status: 201, headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT:
        "Vui lòng nhập đầy đủ tên người nhận, số điện thoại, tỉnh/thành, phường/xã và địa chỉ cụ thể.",
    },
  }
);
