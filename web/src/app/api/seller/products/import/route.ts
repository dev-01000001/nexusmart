import { MAX_IMPORT_CSV_BYTES } from "@/lib/a8-types";
import { GatewayError } from "@/lib/server/errors";
import { gwRaw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { COD_IMPORT_MULTIPART_OVERHEAD_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { assertSellerSession } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

const MAX_UPLOAD_BYTES = MAX_IMPORT_CSV_BYTES + COD_IMPORT_MULTIPART_OVERHEAD_BYTES;

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const bounded = await readBoundedBody(req, MAX_UPLOAD_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    if (bounded.body.byteLength === 0) {
      throw new GatewayError("invalid", "IMPORT_EMPTY_FILE");
    }

    const contentType = req.headers.get("content-type")?.trim() || "text/csv; charset=utf-8";
    const filename = (req.nextUrl.searchParams.get("filename") ?? "").trim().slice(0, 180);

    const upstream = await gwRaw("/api/v1/seller/products/import", {
      method: "POST",
      query: filename ? { filename } : undefined,
      rawBody: new Blob([bounded.body.slice()], { type: contentType }),
      contentType,
      accept: "application/json",
    });

    const data = (await upstream.json()) as unknown;
    return Response.json(data, {
      status: upstream.status,
      headers: { "Cache-Control": "no-store" },
    });
  },
  {
    messages: {
      PAYLOAD_TOO_LARGE: "Tệp CSV vượt quá giới hạn tối đa 1 MB.",
      IMPORT_EMPTY_FILE: "Tệp CSV trống, vui lòng kiểm tra lại dữ liệu.",
    },
  }
);
