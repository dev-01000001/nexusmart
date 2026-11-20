import React, { Suspense } from "react";
import { StateView } from "@/components";
import { loadVouchers } from "@/lib/catalog";
import { VoucherClient } from "./VoucherClient";

export const dynamic = "force-dynamic";

export default async function VoucherPage() {
  const data = await loadVouchers();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải kho mã ưu đãi..." rows={5} />
        </div>
      }
    >
      <VoucherClient vouchers={data.vouchers} shops={data.shops} loadError={data.error} />
    </Suspense>
  );
}
