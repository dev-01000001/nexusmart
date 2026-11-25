import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gwRaw } from "@/lib/server/gateway";
import {
  applyProxySecurityHeaders,
  readBoundedBody,
  resolveMaxBffBodyBytes,
} from "@/lib/server/proxy-security";
import { assertCapability } from "@/lib/server/session";
import type { GatewayImportCodReconciliationResponse } from "@/lib/gateway-types";

const CARRIER_CODE_RE = /^[a-zA-Z0-9_-]{2,40}$/;
const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

export const POST = apiRoute(
  async (req) => {
    await assertCapability("reconciliation.write");
    const contentType = (req.headers.get("content-type") ?? "").trim();
    const lowerCt = contentType.toLowerCase();
    const isMultipart = lowerCt.includes("multipart/form-data");
    const isCsv = lowerCt.includes("text/csv") || lowerCt.includes("text/plain");
    if (!isMultipart && !isCsv) {
      throw new GatewayError("invalid", "UNSUPPORTED_CONTENT_TYPE");
    }

    const maxBytes = resolveMaxBffBodyBytes(
      "POST",
      ["admin", "reconciliation", "cod", "import"],
      contentType
    );
    const bounded = await readBoundedBody(req, maxBytes);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    if (bounded.body.byteLength === 0) {
      throw new GatewayError("invalid", "EMPTY_FILE");
    }

    const q = req.nextUrl.searchParams;
    const carrierCode = (q.get("carrier_code") ?? "").trim();
    const runDate = (q.get("run_date") ?? "").trim();
    const fileName = (q.get("file_name") ?? "").trim().slice(0, 180);

    if (carrierCode && !CARRIER_CODE_RE.test(carrierCode)) {
      throw new GatewayError("invalid", "INVALID_CARRIER_CODE");
    }
    if (runDate && !ISO_DATE_RE.test(runDate)) {
      throw new GatewayError("invalid", "INVALID_DATE");
    }

    const upstream = await gwRaw("/api/v1/admin/reconciliation/cod/import", {
      method: "POST",
      query: {
        carrier_code: carrierCode || undefined,
        run_date: runDate || undefined,
        file_name: fileName || undefined,
      },
      rawBody: new Uint8Array(bounded.body),
      contentType,
      accept: "application/json",
      timeoutMs: 115_000,
    });

    const data = (await upstream.json()) as GatewayImportCodReconciliationResponse;
    const headers = new Headers({ "Cache-Control": "no-store" });
    applyProxySecurityHeaders(headers, upstream.headers, "application/json");
    return Response.json(data, { status: upstream.status, headers });
  },
  {
    messages: {
      UNSUPPORTED_CONTENT_TYPE:
        "Chỉ hỗ trợ tải lên tệp đối soát COD dạng multipart/form-data hoặc text/csv.",
      EMPTY_FILE: "Tệp CSV đối soát COD đang trống.",
      INVALID_CARRIER_CODE: "Mã hãng vận chuyển không hợp lệ.",
      INVALID_DATE: "Ngày đối soát cần theo định dạng YYYY-MM-DD.",
    },
  }
);
