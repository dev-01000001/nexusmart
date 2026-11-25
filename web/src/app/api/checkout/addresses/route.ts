import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import type { GwAddress } from "@/features/commerce/types";

interface CreateAddressBody {
  recipient_name?: string;
  recipient_phone?: string;
  province_code?: string;
  province_name?: string;
  ward_code?: string;
  ward_name?: string;
  street_address?: string;
  legacy_province_code?: string;
  legacy_district_code?: string;
  legacy_ward_code?: string;
  is_default?: boolean;
}

export const GET = apiRoute(async (req) => {
  await requireRequestSite(req);
  await assertSession();
  const data = await gw<{ addresses: GwAddress[] }>("/api/v1/users/me/addresses");
  return Response.json(
    { addresses: data.addresses ?? [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute(
  async (req) => {
    await requireRequestSite(req);
    await assertSession();
    const body = await readJson<CreateAddressBody>(req);
    const recipientName = (body.recipient_name ?? "").trim();
    const recipientPhone = (body.recipient_phone ?? "").trim();
    const provinceCode = (body.province_code ?? "").trim();
    const provinceName = (body.province_name ?? "").trim();
    const wardCode = (body.ward_code ?? "").trim();
    const wardName = (body.ward_name ?? "").trim();
    const streetAddress = (body.street_address ?? "").trim();

    if (
      recipientName.length < 2 ||
      recipientPhone.replace(/\D/g, "").length < 9 ||
      !provinceCode ||
      !wardCode ||
      streetAddress.length < 3
    ) {
      throw new GatewayError("invalid", "INVALID_ADDRESS");
    }

    const data = await gw<{ address: GwAddress }>("/api/v1/users/me/addresses", {
      method: "POST",
      body: {
        recipient_name: recipientName,
        recipient_phone: recipientPhone,
        province_code: provinceCode,
        province_name: provinceName,
        ward_code: wardCode,
        ward_name: wardName,
        street_address: streetAddress,
        ...(body.legacy_province_code
          ? { legacy_province_code: body.legacy_province_code.trim() }
          : {}),
        ...(body.legacy_district_code
          ? { legacy_district_code: body.legacy_district_code.trim() }
          : {}),
        ...(body.legacy_ward_code ? { legacy_ward_code: body.legacy_ward_code.trim() } : {}),
        is_default: Boolean(body.is_default),
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ADDRESS:
        "Vui lòng nhập đầy đủ họ tên, số điện thoại, Tỉnh/Thành phố, Phường/Xã và địa chỉ cụ thể.",
    },
  }
);
